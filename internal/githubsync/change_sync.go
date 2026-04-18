package githubsync

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	gh "github.com/dutifuldev/ghreplica/internal/github"
	"github.com/dutifuldev/ghreplica/internal/gitindex"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	changeBackfillModeOff                 = "off"
	changeBackfillModeOpenOnly            = "open_only"
	defaultTargetedRefreshBurstMaxPRs     = 50
	defaultTargetedRefreshBurstMaxRuntime = 30 * time.Second
	defaultInventoryWriteBatchSize        = 250
)

type RepoBackfillOptions struct {
	MaxPRs     int
	MaxRuntime time.Duration
}

type RepoBackfillResult struct {
	ProcessedPRs   int
	IndexedPRs     int
	FailedPRs      int
	OpenPRTotal    int
	OpenPRCurrent  int
	OpenPRStale    int
	OpenPRMissing  int
	Completed      bool
	NextCursorNum  *int
	NextCursorTime *time.Time
}

type backfillCandidate struct {
	inventory database.RepoOpenPullInventory
}

type ChangeSyncWorker struct {
	db                      *gorm.DB
	service                 *Service
	leases                  *repoLeaseManager
	pollInterval            time.Duration
	webhookRefreshDebounce  time.Duration
	openPRInventoryMaxAge   time.Duration
	leaseTTL                time.Duration
	backfillMaxRuntime      time.Duration
	backfillMaxPRsPerPass   int
	targetedBurstMaxRuntime time.Duration
	targetedBurstMaxPRs     int
}

func NewChangeSyncWorker(db *gorm.DB, service *Service, pollInterval, webhookRefreshDebounce, openPRInventoryMaxAge, leaseTTL, backfillMaxRuntime time.Duration, backfillMaxPRsPerPass int) *ChangeSyncWorker {
	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
	}
	if webhookRefreshDebounce <= 0 {
		webhookRefreshDebounce = 15 * time.Second
	}
	if openPRInventoryMaxAge <= 0 {
		openPRInventoryMaxAge = 10 * time.Minute
	}
	if leaseTTL <= 0 {
		leaseTTL = 15 * time.Minute
	}
	if backfillMaxRuntime <= 0 {
		backfillMaxRuntime = 5 * time.Minute
	}
	if backfillMaxPRsPerPass <= 0 {
		backfillMaxPRsPerPass = 100
	}
	return &ChangeSyncWorker{
		db:                      db,
		service:                 service,
		leases:                  newRepoLeaseManager(db, leaseTTL),
		pollInterval:            pollInterval,
		webhookRefreshDebounce:  webhookRefreshDebounce,
		openPRInventoryMaxAge:   openPRInventoryMaxAge,
		leaseTTL:                leaseTTL,
		backfillMaxRuntime:      backfillMaxRuntime,
		backfillMaxPRsPerPass:   backfillMaxPRsPerPass,
		targetedBurstMaxRuntime: defaultTargetedRefreshBurstMaxRuntime,
		targetedBurstMaxPRs:     defaultTargetedRefreshBurstMaxPRs,
	}
}

func (w *ChangeSyncWorker) Start(ctx context.Context) error {
	slog.Info("change sync worker starting", "owner_id", w.leases.owner())
	if err := w.recoverLeases(ctx); err != nil {
		return err
	}

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		if _, err := w.RunOnce(ctx); err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (w *ChangeSyncWorker) RunOnce(ctx context.Context) (bool, error) {
	processedAny := false
	if processed, err := w.processTargetedRefreshBurst(ctx); err != nil {
		return processedAny, err
	} else if processed {
		processedAny = true
	}
	if processed, err := w.processInventoryScan(ctx, false); err != nil {
		return processedAny || processed, err
	} else if processed {
		return true, nil
	}
	if processed, err := w.processBackfillRepo(ctx); err != nil {
		return processedAny || processed, err
	} else if processed {
		return true, nil
	}
	if processedAny {
		return true, nil
	}
	if processed, err := w.processInventoryScan(ctx, true); err != nil {
		return processed, err
	} else if processed {
		processedAny = true
	}
	return processedAny, nil
}

func (s *Service) ConfigureRepoBackfill(ctx context.Context, owner, repo, mode string, priority int) (database.RepoChangeSyncState, error) {
	mode = normalizeBackfillMode(mode)
	repoResp, err := s.github.GetRepository(ctx, owner, repo)
	if err != nil {
		return database.RepoChangeSyncState{}, err
	}
	canonicalRepo, err := s.upsertRepository(ctx, repoResp)
	if err != nil {
		return database.RepoChangeSyncState{}, err
	}
	now := time.Now().UTC()
	state := database.RepoChangeSyncState{
		RepositoryID:         canonicalRepo.ID,
		Dirty:                true,
		DirtySince:           &now,
		LastRequestedFetchAt: &now,
		BackfillMode:         mode,
		BackfillPriority:     priority,
	}
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "repository_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"dirty":                   true,
			"dirty_since":             now,
			"last_requested_fetch_at": now,
			"backfill_mode":           mode,
			"backfill_priority":       priority,
			"updated_at":              now,
			"last_error":              "",
		}),
	}).Create(&state).Error; err != nil {
		return database.RepoChangeSyncState{}, err
	}
	return s.repoChangeStateByRepositoryID(ctx, canonicalRepo.ID)
}

func (s *Service) NoteRepositoryWebhook(ctx context.Context, repositoryID uint, seenAt time.Time) error {
	if repositoryID == 0 {
		return nil
	}
	seenAt = seenAt.UTC()
	state := database.RepoChangeSyncState{
		RepositoryID:  repositoryID,
		LastWebhookAt: &seenAt,
		BackfillMode:  changeBackfillModeOff,
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "repository_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"last_webhook_at": seenAt,
			"updated_at":      seenAt,
		}),
	}).Create(&state).Error
}

func (s *Service) MarkRepositoryChangeDirty(ctx context.Context, repositoryID uint, seenAt time.Time) error {
	return s.MarkInventoryNeedsRefresh(ctx, repositoryID, seenAt)
}

func retainedDirtyExpr(scanStartedAt time.Time) clause.Expr {
	return gorm.Expr("CASE WHEN dirty_since IS NOT NULL AND dirty_since > ? THEN TRUE ELSE FALSE END", scanStartedAt)
}

func retainedDirtySinceExpr(scanStartedAt time.Time) clause.Expr {
	return gorm.Expr("CASE WHEN dirty_since IS NOT NULL AND dirty_since > ? THEN dirty_since ELSE NULL END", scanStartedAt)
}

func (s *Service) MarkInventoryNeedsRefresh(ctx context.Context, repositoryID uint, seenAt time.Time) error {
	if repositoryID == 0 {
		return nil
	}
	seenAt = seenAt.UTC()
	state := database.RepoChangeSyncState{
		RepositoryID:         repositoryID,
		Dirty:                true,
		DirtySince:           &seenAt,
		LastWebhookAt:        &seenAt,
		LastRequestedFetchAt: &seenAt,
		BackfillMode:         changeBackfillModeOff,
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "repository_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"dirty":                   true,
			"dirty_since":             seenAt,
			"last_webhook_at":         seenAt,
			"last_requested_fetch_at": seenAt,
			"updated_at":              seenAt,
		}),
	}).Create(&state).Error
}

func (s *Service) EnqueuePullRequestRefresh(ctx context.Context, repositoryID uint, number int, seenAt time.Time) error {
	if repositoryID == 0 || number <= 0 {
		return nil
	}
	seenAt = seenAt.UTC()
	if err := s.NoteRepositoryWebhook(ctx, repositoryID, seenAt); err != nil {
		return err
	}
	row := database.RepoTargetedPullRefresh{
		RepositoryID:      repositoryID,
		PullRequestNumber: number,
		RequestedAt:       &seenAt,
		LastWebhookAt:     &seenAt,
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "repository_id"}, {Name: "pull_request_number"}},
		DoUpdates: clause.Assignments(map[string]any{
			"requested_at":    seenAt,
			"last_webhook_at": seenAt,
			"updated_at":      seenAt,
			"last_error":      "",
		}),
	}).Create(&row).Error
}

func (s *Service) GetRepoChangeStatus(ctx context.Context, owner, repo string) (gitindex.RepoStatus, error) {
	repository, err := findRepositoryByName(ctx, s.db, owner, repo)
	if err != nil {
		return gitindex.RepoStatus{}, err
	}
	state, err := s.repoChangeStateOptional(ctx, repository.ID)
	if err != nil {
		return gitindex.RepoStatus{}, err
	}

	status := gitindex.RepoStatus{
		RepositoryID: repository.ID,
		FullName:     repository.FullName,
		BackfillMode: changeBackfillModeOff,
	}
	if state != nil {
		now := time.Now().UTC()
		status.LastWebhookAt = state.LastWebhookAt
		status.LastInventoryScanStartedAt = state.LastFetchStartedAt
		status.LastInventoryScanFinishedAt = state.LastFetchFinishedAt
		status.LastInventoryScanSucceededAt = state.LastSuccessfulFetchAt
		status.LastBackfillStartedAt = state.LastBackfillStartedAt
		status.LastBackfillFinishedAt = state.LastBackfillFinishedAt
		status.BackfillMode = normalizeBackfillMode(state.BackfillMode)
		status.BackfillPriority = state.BackfillPriority
		status.InventoryGenerationCurrent = state.InventoryGenerationCurrent
		status.InventoryGenerationBuilding = state.InventoryGenerationBuilding
		status.InventoryNeedsRefresh = state.Dirty
		status.InventoryLastCommittedAt = state.InventoryLastCommittedAt
		status.InventoryScanRunning = leaseIsActive(now, state.FetchLeaseHeartbeatAt, state.FetchLeaseUntil)
		status.BackfillRunning = leaseIsActive(now, state.BackfillLeaseHeartbeatAt, state.BackfillLeaseUntil)
		status.BackfillGeneration = state.BackfillGeneration
		status.OpenPRTotal = state.OpenPRTotal
		status.OpenPRCurrent = state.OpenPRCurrent
		status.OpenPRStale = state.OpenPRStale
		status.OpenPRMissing = maxInt(0, state.OpenPRTotal-state.OpenPRCurrent-state.OpenPRStale)
		status.BackfillCursor = state.OpenPRCursorNumber
		status.BackfillCursorUpdatedAt = state.OpenPRCursorUpdatedAt
		status.LastError = state.LastError
	}
	pending, running, err := s.targetedRefreshStatus(ctx, repository.ID)
	if err != nil {
		return gitindex.RepoStatus{}, err
	}
	status.TargetedRefreshPending = pending
	status.TargetedRefreshRunning = running
	return status, nil
}

func (s *Service) GetPullRequestChangeStatus(ctx context.Context, owner, repo string, number int) (gitindex.PullRequestStatus, error) {
	repository, err := findRepositoryByName(ctx, s.db, owner, repo)
	if err != nil {
		return gitindex.PullRequestStatus{}, err
	}
	status := gitindex.PullRequestStatus{
		RepositoryID:      repository.ID,
		PullRequestNumber: number,
	}

	var pull database.PullRequest
	err = s.db.WithContext(ctx).
		Where("repository_id = ? AND number = ?", repository.ID, number).
		First(&pull).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return gitindex.PullRequestStatus{}, err
	}
	if err == nil {
		status.State = pull.State
		status.Draft = pull.Draft
	}

	var snapshot database.PullRequestChangeSnapshot
	err = s.db.WithContext(ctx).
		Where("repository_id = ? AND pull_request_number = ?", repository.ID, number).
		First(&snapshot).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return gitindex.PullRequestStatus{}, err
	}
	if err == nil {
		status.Indexed = true
		status.HeadSHA = snapshot.HeadSHA
		status.BaseSHA = snapshot.BaseSHA
		status.MergeBaseSHA = snapshot.MergeBaseSHA
		status.BaseRef = snapshot.BaseRef
		status.IndexedAs = snapshot.IndexedAs
		status.IndexFreshness = snapshot.IndexFreshness
		status.LastIndexedAt = snapshot.LastIndexedAt
		status.ChangedFiles = snapshot.PathCount
		status.IndexedFileCount = snapshot.IndexedFileCount
		status.PathOnlyFileCount = maxInt(0, snapshot.PathCount-snapshot.IndexedFileCount)
		status.HunkCount = snapshot.HunkCount
		status.Additions = snapshot.Additions
		status.Deletions = snapshot.Deletions
		status.PatchBytes = snapshot.PatchBytes

		var skipped int64
		if err := s.db.WithContext(ctx).
			Model(&database.PullRequestChangeFile{}).
			Where("snapshot_id = ? AND indexed_as <> ?", snapshot.ID, "full").
			Count(&skipped).Error; err != nil {
			return gitindex.PullRequestStatus{}, err
		}
		status.SkippedFileCount = int(skipped)
	}

	state, err := s.repoChangeStateOptional(ctx, repository.ID)
	if err != nil {
		return gitindex.PullRequestStatus{}, err
	}
	if state != nil {
		now := time.Now().UTC()
		status.BackfillInProgress = leaseIsActive(now, state.BackfillLeaseHeartbeatAt, state.BackfillLeaseUntil)
		status.InventoryNeedsRefresh = state.Dirty
		status.LastError = state.LastError
	}
	return status, nil
}

func (s *Service) BackfillOpenPullRequests(ctx context.Context, owner, repo string, state database.RepoChangeSyncState, options RepoBackfillOptions) (RepoBackfillResult, error) {
	if s.git == nil {
		return RepoBackfillResult{}, errors.New("git index service is not configured")
	}
	if options.MaxPRs <= 0 {
		options.MaxPRs = 25
	}
	if options.MaxRuntime <= 0 {
		options.MaxRuntime = 3 * time.Minute
	}

	repository, err := findRepositoryByName(ctx, s.db, owner, repo)
	if err != nil {
		return RepoBackfillResult{}, err
	}
	if state.BackfillGeneration <= 0 {
		return RepoBackfillResult{}, nil
	}

	candidates, err := s.listBackfillCandidatesFromInventory(ctx, repository.ID, state.BackfillGeneration, state.OpenPRCursorUpdatedAt, state.OpenPRCursorNumber, options.MaxPRs)
	if err != nil {
		return RepoBackfillResult{}, err
	}
	stateCounts, err := s.repoChangeStateByRepositoryID(ctx, repository.ID)
	if err != nil {
		return RepoBackfillResult{}, err
	}
	result := RepoBackfillResult{
		OpenPRTotal:   stateCounts.OpenPRTotal,
		OpenPRCurrent: stateCounts.OpenPRCurrent,
		OpenPRStale:   stateCounts.OpenPRStale,
		OpenPRMissing: maxInt(0, stateCounts.OpenPRTotal-stateCounts.OpenPRCurrent-stateCounts.OpenPRStale),
	}
	deadline := time.Now().UTC().Add(options.MaxRuntime)
	var lastProcessed *database.RepoOpenPullInventory
	for _, candidate := range candidates {
		if result.ProcessedPRs >= options.MaxPRs || time.Now().UTC().After(deadline) {
			break
		}
		result.ProcessedPRs++
		newFreshness := candidate.inventory.FreshnessState
		updatedInventory := candidate.inventory
		pull, err := s.syncPullRequestChangeOnly(ctx, owner, repo, repository, candidate.inventory.PullRequestNumber)
		if err != nil {
			result.FailedPRs++
			if strings.TrimSpace(newFreshness) == "" {
				newFreshness = "failed"
			}
		} else {
			result.IndexedPRs++
			updatedInventory = inventoryFromPull(repository.ID, pull)
			updatedInventory.Generation = candidate.inventory.Generation
			var freshnessErr error
			newFreshness, freshnessErr = s.reconcileInventoryFreshness(ctx, repository.ID, updatedInventory)
			if freshnessErr != nil {
				return RepoBackfillResult{}, freshnessErr
			}
		}
		if err := s.advanceBackfillProgress(ctx, state.ID, updatedInventory, newFreshness); err != nil {
			return RepoBackfillResult{}, err
		}
		candidateCopy := updatedInventory
		lastProcessed = &candidateCopy
	}

	if lastProcessed != nil {
		more, err := s.hasBackfillCandidatesAfterCursor(ctx, repository.ID, state.BackfillGeneration, lastProcessed.GitHubUpdatedAt, lastProcessed.PullRequestNumber)
		if err != nil {
			return RepoBackfillResult{}, err
		}
		if more {
			result.Completed = false
			result.NextCursorNum = intPtr(lastProcessed.PullRequestNumber)
			nextTime := lastProcessed.GitHubUpdatedAt.UTC()
			result.NextCursorTime = &nextTime
		} else {
			result.Completed = true
		}
	} else {
		any, err := s.hasAnyBackfillCandidates(ctx, repository.ID, state.BackfillGeneration)
		if err != nil {
			return RepoBackfillResult{}, err
		}
		result.Completed = !any
	}

	finalState, err := s.repoChangeStateByRepositoryID(ctx, repository.ID)
	if err != nil {
		return RepoBackfillResult{}, err
	}
	result.OpenPRTotal = finalState.OpenPRTotal
	result.OpenPRCurrent = finalState.OpenPRCurrent
	result.OpenPRStale = finalState.OpenPRStale
	result.OpenPRMissing = maxInt(0, finalState.OpenPRTotal-finalState.OpenPRCurrent-finalState.OpenPRStale)
	if result.Completed {
		result.NextCursorNum = nil
		result.NextCursorTime = nil
	}
	return result, nil
}

func (s *Service) syncPullRequestChangeOnly(ctx context.Context, owner, repo string, canonicalRepo database.Repository, number int) (gh.PullRequestResponse, error) {
	issue, err := s.github.GetIssue(ctx, owner, repo, number)
	if err != nil {
		return gh.PullRequestResponse{}, err
	}
	if _, err := s.upsertIssue(ctx, canonicalRepo.ID, issue); err != nil {
		return gh.PullRequestResponse{}, err
	}
	pull, err := s.github.GetPullRequest(ctx, owner, repo, number)
	if err != nil {
		return gh.PullRequestResponse{}, err
	}
	if err := s.upsertPullRequest(ctx, canonicalRepo.ID, pull); err != nil {
		return gh.PullRequestResponse{}, err
	}
	if err := s.SyncPullRequestIndex(ctx, owner, repo, canonicalRepo.ID, pull); err != nil {
		return gh.PullRequestResponse{}, err
	}
	return pull, nil
}

func (w *ChangeSyncWorker) processTargetedRefreshBurst(ctx context.Context) (bool, error) {
	deadline := time.Now().UTC().Add(w.targetedBurstMaxRuntime)
	processedAny := false
	for processed := 0; processed < w.targetedBurstMaxPRs; processed++ {
		if time.Now().UTC().After(deadline) {
			break
		}
		row, ok, err := w.acquireNextTargetedRefresh(ctx)
		if err != nil {
			return processedAny, err
		}
		if !ok {
			break
		}
		processedAny = true
		if err := w.runTargetedRefresh(ctx, row); err != nil {
			slog.Warn("targeted pull refresh failed", "repository_id", row.RepositoryID, "pull_request_number", row.PullRequestNumber, "error", err)
		}
	}
	return processedAny, nil
}

func (w *ChangeSyncWorker) processInventoryScan(ctx context.Context, ageOnly bool) (bool, error) {
	now := time.Now().UTC()
	fetchAvailableSQL, fetchAvailableArgs := w.leases.reclaimableSQL(fetchLeaseKind, now)
	backfillAvailableSQL, backfillAvailableArgs := w.leases.reclaimableSQL(backfillLeaseKind, now)
	query := w.db.WithContext(ctx).
		Where("backfill_mode <> ?", changeBackfillModeOff).
		Where(fetchAvailableSQL, fetchAvailableArgs...).
		Where(backfillAvailableSQL, backfillAvailableArgs...)
	if ageOnly {
		query = query.
			Where("dirty = ?", false).
			Where("inventory_generation_current <> 0").
			Where("inventory_last_committed_at IS NOT NULL AND inventory_last_committed_at <= ?", now.Add(-w.openPRInventoryMaxAge)).
			Order("backfill_priority DESC, inventory_last_committed_at ASC, repository_id ASC")
	} else {
		query = query.
			Where("((dirty = ? AND (last_requested_fetch_at IS NULL OR last_requested_fetch_at <= ?)) OR inventory_generation_current = 0 OR inventory_last_committed_at IS NULL)",
				true,
				now.Add(-w.webhookRefreshDebounce),
			).
			Order("CASE WHEN inventory_generation_current = 0 THEN 0 ELSE 1 END ASC, backfill_priority DESC, inventory_last_committed_at ASC NULLS FIRST, repository_id ASC")
	}
	var state database.RepoChangeSyncState
	err := query.First(&state).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}

	acquired, leasedUntil, err := w.leases.acquire(ctx, state.ID, fetchLeaseKind, now)
	if err != nil {
		return false, err
	}
	if !acquired {
		return false, nil
	}
	nextGeneration := nextInventoryGeneration(state)
	if state.InventoryGenerationBuilding != nil && *state.InventoryGenerationBuilding > 0 {
		nextGeneration = *state.InventoryGenerationBuilding
	}
	if err := w.db.WithContext(ctx).Model(&database.RepoChangeSyncState{}).
		Where("id = ? AND fetch_lease_owner_id = ?", state.ID, w.leases.owner()).
		Updates(map[string]any{
			"last_fetch_started_at":         now,
			"inventory_generation_building": nextGeneration,
			"updated_at":                    now,
		}).Error; err != nil {
		return false, err
	}
	slog.Info("change sync fetch lease acquired", "state_id", state.ID, "repository_id", state.RepositoryID, "owner_id", w.leases.owner(), "lease_until", leasedUntil)
	state.FetchLeaseOwnerID = w.leases.owner()
	state.FetchLeaseStartedAt = &now
	state.FetchLeaseHeartbeatAt = &now
	state.FetchLeaseUntil = leasedUntil
	state.InventoryGenerationBuilding = intPtr(nextGeneration)
	return true, w.runFetchPass(ctx, state)
}

func (w *ChangeSyncWorker) processBackfillRepo(ctx context.Context) (bool, error) {
	now := time.Now().UTC()
	backfillAvailableSQL, backfillAvailableArgs := w.leases.reclaimableSQL(backfillLeaseKind, now)
	fetchAvailableSQL, fetchAvailableArgs := w.leases.reclaimableSQL(fetchLeaseKind, now)
	var state database.RepoChangeSyncState
	err := w.db.WithContext(ctx).
		Where("backfill_mode <> ?", changeBackfillModeOff).
		Where("backfill_generation > 0").
		Where("open_pr_current < open_pr_total OR open_pr_stale > 0").
		Where(backfillAvailableSQL, backfillAvailableArgs...).
		Where(fetchAvailableSQL, fetchAvailableArgs...).
		Order("backfill_priority DESC, last_backfill_finished_at ASC NULLS FIRST, repository_id ASC").
		First(&state).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}

	acquired, leasedUntil, err := w.leases.acquire(ctx, state.ID, backfillLeaseKind, now)
	if err != nil {
		return false, err
	}
	if !acquired {
		return false, nil
	}
	if err := w.db.WithContext(ctx).Model(&database.RepoChangeSyncState{}).
		Where("id = ? AND backfill_lease_owner_id = ?", state.ID, w.leases.owner()).
		Updates(map[string]any{
			"last_backfill_started_at": now,
			"updated_at":               now,
		}).Error; err != nil {
		return false, err
	}
	slog.Info("change sync backfill lease acquired", "state_id", state.ID, "repository_id", state.RepositoryID, "owner_id", w.leases.owner(), "lease_until", leasedUntil)
	state.BackfillLeaseOwnerID = w.leases.owner()
	state.BackfillLeaseStartedAt = &now
	state.BackfillLeaseHeartbeatAt = &now
	state.BackfillLeaseUntil = leasedUntil
	return true, w.runBackfillPass(ctx, state, false)
}

func (w *ChangeSyncWorker) runFetchPass(ctx context.Context, state database.RepoChangeSyncState) error {
	var result RepoBackfillResult
	runErr := w.runWithLeaseHeartbeat(ctx, state.ID, fetchLeaseKind, func(passCtx context.Context) error {
		repository, err := repositoryByID(passCtx, w.db, state.RepositoryID)
		if err != nil {
			return err
		}
		owner, name, err := splitFullName(repository.FullName)
		if err != nil {
			return err
		}

		result, err = w.service.syncOpenPullInventory(passCtx, owner, name, state)
		return err
	})
	if runErr != nil {
		return w.finishFetchStateWithError(ctx, state, runErr)
	}
	return w.completeFetchPass(ctx, state, result)
}

func (w *ChangeSyncWorker) runBackfillPass(ctx context.Context, state database.RepoChangeSyncState, _ bool) error {
	var updates map[string]any
	runErr := w.runWithLeaseHeartbeat(ctx, state.ID, backfillLeaseKind, func(passCtx context.Context) error {
		repository, err := repositoryByID(passCtx, w.db, state.RepositoryID)
		if err != nil {
			return err
		}
		owner, name, err := splitFullName(repository.FullName)
		if err != nil {
			return err
		}

		result, err := w.service.BackfillOpenPullRequests(passCtx, owner, name, state, RepoBackfillOptions{
			MaxPRs:     w.backfillMaxPRsPerPass,
			MaxRuntime: w.backfillMaxRuntime,
		})
		if err != nil {
			return err
		}
		updates = map[string]any{
			"last_error":                "",
			"open_pr_cursor_number":     result.NextCursorNum,
			"open_pr_cursor_updated_at": result.NextCursorTime,
		}
		return nil
	})
	if runErr != nil {
		return w.finishBackfillStateWithError(ctx, state, runErr)
	}
	return w.completeBackfillPass(ctx, state, updates)
}

func (w *ChangeSyncWorker) completeFetchPass(ctx context.Context, state database.RepoChangeSyncState, result RepoBackfillResult) error {
	now := time.Now().UTC()
	updates := map[string]any{
		"last_error":                    "",
		"last_fetch_finished_at":        now,
		"last_successful_fetch_at":      now,
		"inventory_generation_building": nil,
		"updated_at":                    now,
	}
	if err := w.leases.release(ctx, state.ID, fetchLeaseKind, updates); err != nil {
		return err
	}
	slog.Info("change sync fetch pass completed", "state_id", state.ID, "repository_id", state.RepositoryID, "owner_id", w.leases.owner(), "open_pr_total", result.OpenPRTotal, "open_pr_current", result.OpenPRCurrent, "open_pr_stale", result.OpenPRStale)
	return nil
}

func (w *ChangeSyncWorker) completeBackfillPass(ctx context.Context, state database.RepoChangeSyncState, updates map[string]any) error {
	now := time.Now().UTC()
	updates["last_backfill_finished_at"] = now
	if err := w.leases.release(ctx, state.ID, backfillLeaseKind, updates); err != nil {
		return err
	}
	slog.Info("change sync backfill pass completed", "state_id", state.ID, "repository_id", state.RepositoryID, "owner_id", w.leases.owner())
	return nil
}

func (w *ChangeSyncWorker) finishFetchStateWithError(ctx context.Context, state database.RepoChangeSyncState, runErr error) error {
	now := time.Now().UTC()
	updates := map[string]any{
		"last_error":                    runErr.Error(),
		"last_fetch_finished_at":        now,
		"inventory_generation_building": nil,
	}
	if err := w.leases.release(ctx, state.ID, fetchLeaseKind, updates); err != nil {
		return err
	}
	slog.Warn("change sync fetch pass failed", "state_id", state.ID, "repository_id", state.RepositoryID, "owner_id", w.leases.owner(), "error", runErr)
	return runErr
}

func (w *ChangeSyncWorker) finishBackfillStateWithError(ctx context.Context, state database.RepoChangeSyncState, runErr error) error {
	now := time.Now().UTC()
	updates := map[string]any{
		"last_error":                runErr.Error(),
		"last_backfill_finished_at": now,
	}
	if err := w.leases.release(ctx, state.ID, backfillLeaseKind, updates); err != nil {
		return err
	}
	slog.Warn("change sync backfill pass failed", "state_id", state.ID, "repository_id", state.RepositoryID, "owner_id", w.leases.owner(), "error", runErr)
	return runErr
}

func (w *ChangeSyncWorker) acquireNextTargetedRefresh(ctx context.Context) (database.RepoTargetedPullRefresh, bool, error) {
	now := time.Now().UTC()
	staleBefore := now.Add(-w.leases.staleAfter)
	retryBefore := now.Add(-time.Minute)
	var row database.RepoTargetedPullRefresh
	err := w.db.WithContext(ctx).
		Where("requested_at IS NOT NULL").
		Where("(last_completed_at IS NULL OR requested_at > last_completed_at)").
		Where("(last_attempted_at IS NULL OR last_attempted_at <= ?)", retryBefore).
		Where("(lease_until IS NULL OR lease_until <= ? OR lease_heartbeat_at IS NULL OR lease_heartbeat_at <= ?)", now, staleBefore).
		Order("requested_at ASC, repository_id ASC, pull_request_number ASC").
		First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return database.RepoTargetedPullRefresh{}, false, nil
		}
		return database.RepoTargetedPullRefresh{}, false, err
	}
	leaseUntil := now.Add(w.leaseTTL)
	result := w.db.WithContext(ctx).Model(&database.RepoTargetedPullRefresh{}).
		Where("id = ?", row.ID).
		Where("(lease_until IS NULL OR lease_until <= ? OR lease_heartbeat_at IS NULL OR lease_heartbeat_at <= ?)", now, staleBefore).
		Updates(map[string]any{
			"lease_owner_id":     w.leases.owner(),
			"lease_started_at":   now,
			"lease_heartbeat_at": now,
			"lease_until":        leaseUntil,
			"last_attempted_at":  now,
			"updated_at":         now,
		})
	if result.Error != nil {
		return database.RepoTargetedPullRefresh{}, false, result.Error
	}
	if result.RowsAffected == 0 {
		return database.RepoTargetedPullRefresh{}, false, nil
	}
	row.LeaseOwnerID = w.leases.owner()
	row.LeaseStartedAt = &now
	row.LeaseHeartbeatAt = &now
	row.LeaseUntil = &leaseUntil
	row.LastAttemptedAt = &now
	return row, true, nil
}

func (w *ChangeSyncWorker) runTargetedRefresh(ctx context.Context, row database.RepoTargetedPullRefresh) error {
	repository, err := repositoryByID(ctx, w.db, row.RepositoryID)
	if err != nil {
		return w.finishTargetedRefresh(ctx, row, err)
	}
	owner, name, err := splitFullName(repository.FullName)
	if err != nil {
		return w.finishTargetedRefresh(ctx, row, err)
	}
	pull, err := w.service.syncPullRequestChangeOnly(ctx, owner, name, repository, row.PullRequestNumber)
	if err == nil {
		err = w.service.reconcileTargetedRefresh(ctx, repository.ID, row.PullRequestNumber, pull)
	}
	return w.finishTargetedRefresh(ctx, row, err)
}

func (w *ChangeSyncWorker) finishTargetedRefresh(ctx context.Context, row database.RepoTargetedPullRefresh, refreshErr error) error {
	now := time.Now().UTC()
	updates := map[string]any{
		"lease_owner_id":     "",
		"lease_started_at":   nil,
		"lease_heartbeat_at": nil,
		"lease_until":        nil,
		"updated_at":         now,
	}
	if refreshErr == nil {
		updates["last_completed_at"] = now
		updates["last_error"] = ""
	} else {
		updates["last_error"] = refreshErr.Error()
		if err := w.db.WithContext(ctx).Model(&database.RepoChangeSyncState{}).
			Where("repository_id = ?", row.RepositoryID).
			Updates(map[string]any{
				"last_error": refreshErr.Error(),
				"updated_at": now,
			}).Error; err != nil {
			return err
		}
	}
	if err := w.db.WithContext(ctx).Model(&database.RepoTargetedPullRefresh{}).
		Where("id = ? AND lease_owner_id = ?", row.ID, w.leases.owner()).
		Updates(updates).Error; err != nil {
		return err
	}
	return refreshErr
}

func (s *Service) targetedRefreshStatus(ctx context.Context, repositoryID uint) (bool, bool, error) {
	var pendingCount int64
	if err := s.db.WithContext(ctx).Model(&database.RepoTargetedPullRefresh{}).
		Where("repository_id = ?", repositoryID).
		Where("requested_at IS NOT NULL").
		Where("(last_completed_at IS NULL OR requested_at > last_completed_at)").
		Count(&pendingCount).Error; err != nil {
		return false, false, err
	}
	now := time.Now().UTC()
	staleAfter := maxDuration(3*changeSyncHeartbeatInterval(15*time.Minute), time.Second)
	var runningCount int64
	if err := s.db.WithContext(ctx).Model(&database.RepoTargetedPullRefresh{}).
		Where("repository_id = ?", repositoryID).
		Where("lease_until IS NOT NULL AND lease_until > ?", now).
		Where("lease_heartbeat_at IS NOT NULL AND lease_heartbeat_at > ?", now.Add(-staleAfter)).
		Count(&runningCount).Error; err != nil {
		return false, false, err
	}
	return pendingCount > 0, runningCount > 0, nil
}

func (s *Service) repoChangeStateByRepositoryID(ctx context.Context, repositoryID uint) (database.RepoChangeSyncState, error) {
	var state database.RepoChangeSyncState
	err := s.db.WithContext(ctx).Where("repository_id = ?", repositoryID).First(&state).Error
	return state, err
}

func (s *Service) repoChangeStateOptional(ctx context.Context, repositoryID uint) (*database.RepoChangeSyncState, error) {
	var state database.RepoChangeSyncState
	err := s.db.WithContext(ctx).Where("repository_id = ?", repositoryID).First(&state).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &state, nil
}

func (s *Service) pullRequestSnapshotMap(ctx context.Context, repositoryID uint) (map[int]*database.PullRequestChangeSnapshot, error) {
	var snapshots []database.PullRequestChangeSnapshot
	if err := s.db.WithContext(ctx).Where("repository_id = ?", repositoryID).Find(&snapshots).Error; err != nil {
		return nil, err
	}
	out := make(map[int]*database.PullRequestChangeSnapshot, len(snapshots))
	for i := range snapshots {
		snapshot := snapshots[i]
		out[snapshot.PullRequestNumber] = &snapshot
	}
	return out, nil
}

func findRepositoryByName(ctx context.Context, db *gorm.DB, owner, repo string) (database.Repository, error) {
	var repository database.Repository
	err := db.WithContext(ctx).
		Where("full_name = ?", strings.TrimSpace(owner)+"/"+strings.TrimSpace(repo)).
		First(&repository).Error
	return repository, err
}

func repositoryByID(ctx context.Context, db *gorm.DB, repositoryID uint) (database.Repository, error) {
	var repository database.Repository
	err := db.WithContext(ctx).Where("id = ?", repositoryID).First(&repository).Error
	return repository, err
}

func desiredFreshness(snapshot *database.PullRequestChangeSnapshot, pull gh.PullRequestResponse) string {
	if snapshot == nil {
		return ""
	}
	if snapshot.HeadSHA != strings.TrimSpace(pull.Head.SHA) {
		return "stale_head_changed"
	}
	if snapshot.BaseSHA != strings.TrimSpace(pull.Base.SHA) || normalizeBackfillBaseRef(snapshot.BaseRef) != normalizeBackfillBaseRef(pull.Base.Ref) {
		return "stale_base_moved"
	}
	if strings.TrimSpace(snapshot.IndexFreshness) == "" {
		return "stale_head_changed"
	}
	if snapshot.IndexFreshness != "current" {
		return snapshot.IndexFreshness
	}
	return "current"
}

func normalizeBackfillMode(mode string) string {
	switch strings.TrimSpace(mode) {
	case "", changeBackfillModeOff:
		return changeBackfillModeOff
	case changeBackfillModeOpenOnly:
		return changeBackfillModeOpenOnly
	default:
		return changeBackfillModeOpenOnly
	}
}

func (s *Service) syncOpenPullInventory(ctx context.Context, owner, repo string, state database.RepoChangeSyncState) (RepoBackfillResult, error) {
	scanStartedAt := time.Now().UTC()
	if state.FetchLeaseStartedAt != nil && !state.FetchLeaseStartedAt.IsZero() {
		scanStartedAt = state.FetchLeaseStartedAt.UTC()
	}
	openPulls, err := s.github.ListPullRequests(ctx, owner, repo, "open")
	if err != nil {
		return RepoBackfillResult{}, err
	}
	sort.Slice(openPulls, func(i, j int) bool {
		if openPulls[i].UpdatedAt.Equal(openPulls[j].UpdatedAt) {
			return openPulls[i].Number > openPulls[j].Number
		}
		return openPulls[i].UpdatedAt.After(openPulls[j].UpdatedAt)
	})

	repositoryID := state.RepositoryID
	snapshotMap, err := s.pullRequestSnapshotMap(ctx, repositoryID)
	if err != nil {
		return RepoBackfillResult{}, err
	}
	result := RepoBackfillResult{OpenPRTotal: len(openPulls)}
	now := time.Now().UTC()
	seen := make([]int, 0, len(openPulls))
	inventoryRows := make([]database.RepoOpenPullInventory, 0, len(openPulls))
	snapshotFreshnessUpdates := make(map[string][]uint)
	nextGeneration := nextInventoryGeneration(state)
	if state.InventoryGenerationBuilding != nil && *state.InventoryGenerationBuilding > 0 {
		nextGeneration = *state.InventoryGenerationBuilding
	}

	for _, pull := range openPulls {
		snapshot := snapshotMap[pull.Number]
		freshness := desiredFreshness(snapshot, pull)
		if snapshot != nil && snapshot.IndexFreshness != freshness {
			snapshotFreshnessUpdates[freshness] = append(snapshotFreshnessUpdates[freshness], snapshot.ID)
		}

		inventoryRows = append(inventoryRows, database.RepoOpenPullInventory{
			RepositoryID:      repositoryID,
			Generation:        nextGeneration,
			PullRequestNumber: pull.Number,
			GitHubUpdatedAt:   pull.UpdatedAt.UTC(),
			HeadSHA:           strings.TrimSpace(pull.Head.SHA),
			BaseSHA:           strings.TrimSpace(pull.Base.SHA),
			BaseRef:           strings.TrimSpace(pull.Base.Ref),
			State:             strings.TrimSpace(pull.State),
			Draft:             pull.Draft,
			FreshnessState:    freshness,
			LastSeenAt:        now,
		})

		seen = append(seen, pull.Number)
		result.OpenPRCurrent, result.OpenPRStale = adjustBackfillCounts(result.OpenPRCurrent, result.OpenPRStale, "", freshness)
	}

	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for freshness, snapshotIDs := range snapshotFreshnessUpdates {
			if err := tx.Model(&database.PullRequestChangeSnapshot{}).
				Where("id IN ?", snapshotIDs).
				Updates(map[string]any{
					"index_freshness": freshness,
					"updated_at":      now,
				}).Error; err != nil {
				return err
			}
		}

		if len(inventoryRows) > 0 {
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "repository_id"}, {Name: "generation"}, {Name: "pull_request_number"}},
				DoUpdates: clause.Assignments(map[string]any{
					"github_updated_at": gorm.Expr("excluded.github_updated_at"),
					"head_sha":          gorm.Expr("excluded.head_sha"),
					"base_sha":          gorm.Expr("excluded.base_sha"),
					"base_ref":          gorm.Expr("excluded.base_ref"),
					"state":             gorm.Expr("excluded.state"),
					"draft":             gorm.Expr("excluded.draft"),
					"freshness_state":   gorm.Expr("excluded.freshness_state"),
					"last_seen_at":      gorm.Expr("excluded.last_seen_at"),
					"updated_at":        now,
				}),
			}).CreateInBatches(inventoryRows, defaultInventoryWriteBatchSize).Error; err != nil {
				return err
			}
		}

		prune := tx.Where("repository_id = ? AND generation = ?", repositoryID, nextGeneration)
		if len(seen) > 0 {
			prune = prune.Where("pull_request_number NOT IN ?", seen)
		}
		if err := prune.Delete(&database.RepoOpenPullInventory{}).Error; err != nil {
			return err
		}
		if err := tx.Where("repository_id = ? AND generation <> ?", repositoryID, nextGeneration).
			Delete(&database.RepoOpenPullInventory{}).Error; err != nil {
			return err
		}
		return tx.Model(&database.RepoChangeSyncState{}).
			Where("id = ?", state.ID).
			Updates(map[string]any{
				"dirty":                        retainedDirtyExpr(scanStartedAt),
				"dirty_since":                  retainedDirtySinceExpr(scanStartedAt),
				"inventory_generation_current": nextGeneration,
				"inventory_last_committed_at":  now,
				"backfill_generation":          nextGeneration,
				"open_pr_cursor_number":        nil,
				"open_pr_cursor_updated_at":    nil,
				"open_pr_total":                result.OpenPRTotal,
				"open_pr_current":              result.OpenPRCurrent,
				"open_pr_stale":                result.OpenPRStale,
				"last_open_pr_scan_at":         now,
				"updated_at":                   now,
			}).Error
	}); err != nil {
		return RepoBackfillResult{}, err
	}

	result.OpenPRMissing = maxInt(0, result.OpenPRTotal-result.OpenPRCurrent-result.OpenPRStale)
	return result, nil
}

func normalizeBackfillBaseRef(ref string) string {
	ref = strings.TrimSpace(ref)
	return strings.TrimPrefix(ref, "refs/heads/")
}

func (s *Service) listBackfillCandidatesFromInventory(ctx context.Context, repositoryID uint, generation int, cursorTime *time.Time, cursorNumber *int, limit int) ([]backfillCandidate, error) {
	if limit <= 0 {
		limit = 10
	}
	var inventoryRows []database.RepoOpenPullInventory
	query := s.backfillInventoryQuery(ctx, repositoryID, generation, cursorTime, cursorNumber).
		Order("github_updated_at DESC").
		Order("pull_request_number DESC").
		Limit(limit)
	if err := query.Find(&inventoryRows).Error; err != nil {
		return nil, err
	}
	candidates := make([]backfillCandidate, 0, len(inventoryRows))
	for _, row := range inventoryRows {
		candidates = append(candidates, backfillCandidate{inventory: row})
	}
	return candidates, nil
}

func (s *Service) hasBackfillCandidatesAfterCursor(ctx context.Context, repositoryID uint, generation int, cursorTime time.Time, cursorNumber int) (bool, error) {
	var count int64
	err := s.backfillInventoryQuery(ctx, repositoryID, generation, &cursorTime, &cursorNumber).
		Limit(1).
		Count(&count).Error
	return count > 0, err
}

func (s *Service) hasAnyBackfillCandidates(ctx context.Context, repositoryID uint, generation int) (bool, error) {
	var count int64
	err := s.backfillInventoryQuery(ctx, repositoryID, generation, nil, nil).
		Limit(1).
		Count(&count).Error
	return count > 0, err
}

func (s *Service) backfillInventoryQuery(ctx context.Context, repositoryID uint, generation int, cursorTime *time.Time, cursorNumber *int) *gorm.DB {
	query := s.db.WithContext(ctx).
		Model(&database.RepoOpenPullInventory{}).
		Where("repository_id = ?", repositoryID).
		Where("generation = ?", generation).
		Where("freshness_state <> ?", "current")
	if cursorTime != nil && cursorNumber != nil {
		updatedAt := cursorTime.UTC()
		query = query.Where(
			"(github_updated_at < ?) OR (github_updated_at = ? AND pull_request_number < ?)",
			updatedAt,
			updatedAt,
			*cursorNumber,
		)
	}
	return query
}

func (s *Service) reconcileInventoryFreshness(ctx context.Context, repositoryID uint, inventory database.RepoOpenPullInventory) (string, error) {
	snapshot, err := s.pullRequestSnapshotOptional(ctx, repositoryID, inventory.PullRequestNumber)
	if err != nil {
		return "", err
	}
	if snapshot == nil {
		return "failed", nil
	}
	freshness := desiredInventoryFreshness(snapshot, inventory)
	if snapshot.IndexFreshness != freshness {
		if err := s.db.WithContext(ctx).
			Model(&database.PullRequestChangeSnapshot{}).
			Where("id = ?", snapshot.ID).
			Updates(map[string]any{
				"index_freshness": freshness,
				"updated_at":      time.Now().UTC(),
			}).Error; err != nil {
			return "", err
		}
	}
	return freshness, nil
}

func (s *Service) reconcileTargetedRefresh(ctx context.Context, repositoryID uint, number int, pull gh.PullRequestResponse) error {
	state, err := s.repoChangeStateOptional(ctx, repositoryID)
	if err != nil {
		return err
	}
	if state == nil || state.InventoryGenerationCurrent <= 0 {
		return nil
	}

	now := time.Now().UTC()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var inventory database.RepoOpenPullInventory
		if err := tx.Where("repository_id = ? AND generation = ? AND pull_request_number = ?", repositoryID, state.InventoryGenerationCurrent, number).
			First(&inventory).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}

		snapshot, err := s.pullRequestSnapshotOptional(ctx, repositoryID, number)
		if err != nil {
			return err
		}
		newFreshness := desiredFreshness(snapshot, pull)
		nextCurrent, nextStale := adjustBackfillCounts(state.OpenPRCurrent, state.OpenPRStale, inventory.FreshnessState, newFreshness)
		if err := tx.Model(&database.RepoOpenPullInventory{}).
			Where("id = ?", inventory.ID).
			Updates(map[string]any{
				"github_updated_at": pull.UpdatedAt.UTC(),
				"head_sha":          strings.TrimSpace(pull.Head.SHA),
				"base_sha":          strings.TrimSpace(pull.Base.SHA),
				"base_ref":          strings.TrimSpace(pull.Base.Ref),
				"state":             strings.TrimSpace(pull.State),
				"draft":             pull.Draft,
				"freshness_state":   newFreshness,
				"last_seen_at":      now,
				"updated_at":        now,
			}).Error; err != nil {
			return err
		}
		return tx.Model(&database.RepoChangeSyncState{}).
			Where("id = ?", state.ID).
			Updates(map[string]any{
				"open_pr_current": nextCurrent,
				"open_pr_stale":   nextStale,
				"updated_at":      now,
				"last_error":      "",
			}).Error
	})
}

func inventoryFromPull(repositoryID uint, pull gh.PullRequestResponse) database.RepoOpenPullInventory {
	return database.RepoOpenPullInventory{
		RepositoryID:      repositoryID,
		PullRequestNumber: pull.Number,
		GitHubUpdatedAt:   pull.UpdatedAt.UTC(),
		HeadSHA:           strings.TrimSpace(pull.Head.SHA),
		BaseSHA:           strings.TrimSpace(pull.Base.SHA),
		BaseRef:           strings.TrimSpace(pull.Base.Ref),
		State:             strings.TrimSpace(pull.State),
		Draft:             pull.Draft,
	}
}

func (s *Service) advanceBackfillProgress(ctx context.Context, stateID uint, inventory database.RepoOpenPullInventory, newFreshness string) error {
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var stored database.RepoOpenPullInventory
		if err := tx.Where("repository_id = ? AND generation = ? AND pull_request_number = ?", inventory.RepositoryID, inventory.Generation, inventory.PullRequestNumber).
			First(&stored).Error; err != nil {
			return err
		}
		var state database.RepoChangeSyncState
		if err := tx.Where("id = ?", stateID).First(&state).Error; err != nil {
			return err
		}

		nextCurrent, nextStale := adjustBackfillCounts(state.OpenPRCurrent, state.OpenPRStale, stored.FreshnessState, newFreshness)
		if err := tx.Model(&database.RepoOpenPullInventory{}).
			Where("id = ?", stored.ID).
			Updates(map[string]any{
				"github_updated_at": inventory.GitHubUpdatedAt,
				"head_sha":          inventory.HeadSHA,
				"base_sha":          inventory.BaseSHA,
				"base_ref":          inventory.BaseRef,
				"state":             inventory.State,
				"draft":             inventory.Draft,
				"freshness_state":   newFreshness,
				"updated_at":        now,
			}).Error; err != nil {
			return err
		}
		return tx.Model(&database.RepoChangeSyncState{}).
			Where("id = ?", state.ID).
			Updates(map[string]any{
				"open_pr_current":           nextCurrent,
				"open_pr_stale":             nextStale,
				"open_pr_cursor_number":     inventory.PullRequestNumber,
				"open_pr_cursor_updated_at": inventory.GitHubUpdatedAt.UTC(),
				"updated_at":                now,
			}).Error
	})
}

func (s *Service) pullRequestSnapshotOptional(ctx context.Context, repositoryID uint, number int) (*database.PullRequestChangeSnapshot, error) {
	var snapshot database.PullRequestChangeSnapshot
	err := s.db.WithContext(ctx).
		Where("repository_id = ? AND pull_request_number = ?", repositoryID, number).
		First(&snapshot).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &snapshot, nil
}

func desiredInventoryFreshness(snapshot *database.PullRequestChangeSnapshot, inventory database.RepoOpenPullInventory) string {
	return desiredFreshness(snapshot, gh.PullRequestResponse{
		Head: gh.PullBranch{SHA: inventory.HeadSHA},
		Base: gh.PullBranch{SHA: inventory.BaseSHA, Ref: inventory.BaseRef},
	})
}

func adjustBackfillCounts(current, stale int, oldFreshness, newFreshness string) (int, int) {
	switch backfillFreshnessCategory(oldFreshness) {
	case "current":
		current--
	case "stale":
		stale--
	}
	switch backfillFreshnessCategory(newFreshness) {
	case "current":
		current++
	case "stale":
		stale++
	}
	if current < 0 {
		current = 0
	}
	if stale < 0 {
		stale = 0
	}
	return current, stale
}

func backfillFreshnessCategory(freshness string) string {
	switch strings.TrimSpace(freshness) {
	case "":
		return "missing"
	case "current":
		return "current"
	default:
		return "stale"
	}
}

func nextInventoryGeneration(state database.RepoChangeSyncState) int {
	next := state.InventoryGenerationCurrent + 1
	if state.InventoryGenerationBuilding != nil && *state.InventoryGenerationBuilding >= next {
		next = *state.InventoryGenerationBuilding + 1
	}
	if next <= 0 {
		return 1
	}
	return next
}

func intPtr(v int) *int {
	return &v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
