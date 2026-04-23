package githubsync

import (
	"context"
	"database/sql"
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
	changeBackfillModeOpenAndRecent       = "open_and_recent"
	changeBackfillModeFullHistory         = "full_history"
	defaultTargetedRefreshBurstMaxPRs     = 50
	defaultTargetedRefreshBurstMaxRuntime = 30 * time.Second
	defaultInventoryWriteBatchSize        = 100
	defaultRecentPRRepairInterval         = 24 * time.Hour
	defaultRecentPRRepairWindow           = 7 * 24 * time.Hour
	defaultRecentPRRepairMaxPages         = 2
	defaultRecentPRRepairPerPage          = 100
	defaultFullHistoryRepairInterval      = 10 * time.Minute
	defaultFullHistoryRepairMaxPages      = 10
	defaultFullHistoryRepairPerPage       = 100
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

type changeSyncRepoWorkKind string

const (
	changeSyncRepoWorkNone              changeSyncRepoWorkKind = ""
	changeSyncRepoWorkRecentRepair      changeSyncRepoWorkKind = "recent_pr_repair"
	changeSyncRepoWorkFullHistoryRepair changeSyncRepoWorkKind = "full_history_repair"
	changeSyncRepoWorkInitialInventory  changeSyncRepoWorkKind = "initial_inventory_scan"
	changeSyncRepoWorkBackfill          changeSyncRepoWorkKind = "backfill"
	changeSyncRepoWorkAgedInventory     changeSyncRepoWorkKind = "aged_inventory_scan"
)

type ChangeSyncWorker struct {
	db                        *gorm.DB
	service                   *Service
	leases                    *repoLeaseManager
	pollInterval              time.Duration
	webhookRefreshDebounce    time.Duration
	openPRInventoryMaxAge     time.Duration
	leaseTTL                  time.Duration
	backfillMaxRuntime        time.Duration
	backfillMaxPRsPerPass     int
	targetedBurstMaxRuntime   time.Duration
	targetedBurstMaxPRs       int
	recentPRRepairInterval    time.Duration
	recentPRRepairWindow      time.Duration
	recentPRRepairMaxPages    int
	recentPRRepairPerPage     int
	fullHistoryRepairInterval time.Duration
	fullHistoryRepairMaxPages int
	fullHistoryRepairPerPage  int
}

func NewChangeSyncWorker(db *gorm.DB, service *Service, pollInterval, webhookRefreshDebounce, openPRInventoryMaxAge, leaseTTL, backfillMaxRuntime time.Duration, backfillMaxPRsPerPass int) *ChangeSyncWorker {
	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
	}
	if webhookRefreshDebounce <= 0 {
		webhookRefreshDebounce = 15 * time.Second
	}
	if openPRInventoryMaxAge <= 0 {
		openPRInventoryMaxAge = 6 * time.Hour
	}
	if leaseTTL <= 0 {
		leaseTTL = 15 * time.Minute
	}
	if backfillMaxRuntime <= 0 {
		backfillMaxRuntime = 30 * time.Minute
	}
	if backfillMaxPRsPerPass <= 0 {
		backfillMaxPRsPerPass = 1000
	}
	return &ChangeSyncWorker{
		db:                        db,
		service:                   service,
		leases:                    newRepoLeaseManager(db, leaseTTL),
		pollInterval:              pollInterval,
		webhookRefreshDebounce:    webhookRefreshDebounce,
		openPRInventoryMaxAge:     openPRInventoryMaxAge,
		leaseTTL:                  leaseTTL,
		backfillMaxRuntime:        backfillMaxRuntime,
		backfillMaxPRsPerPass:     backfillMaxPRsPerPass,
		targetedBurstMaxRuntime:   defaultTargetedRefreshBurstMaxRuntime,
		targetedBurstMaxPRs:       defaultTargetedRefreshBurstMaxPRs,
		recentPRRepairInterval:    defaultRecentPRRepairInterval,
		recentPRRepairWindow:      defaultRecentPRRepairWindow,
		recentPRRepairMaxPages:    defaultRecentPRRepairMaxPages,
		recentPRRepairPerPage:     defaultRecentPRRepairPerPage,
		fullHistoryRepairInterval: defaultFullHistoryRepairInterval,
		fullHistoryRepairMaxPages: defaultFullHistoryRepairMaxPages,
		fullHistoryRepairPerPage:  defaultFullHistoryRepairPerPage,
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

	workKind, err := w.nextRepoWork(ctx)
	if err != nil {
		return processedAny, err
	}
	if workKind != changeSyncRepoWorkNone {
		processed, err := w.runRepoWork(ctx, workKind)
		if err != nil {
			return processedAny || processed, err
		}
		if processed {
			return true, nil
		}
	}
	if processedAny {
		return true, nil
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
		RepositoryID:             canonicalRepo.ID,
		Dirty:                    true,
		DirtySince:               &now,
		LastRequestedFetchAt:     &now,
		BackfillMode:             mode,
		BackfillPriority:         priority,
		RecentPRRepairCursorPage: 1,
		FullHistoryCursorPage:    1,
	}
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "repository_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"dirty":                          true,
			"dirty_since":                    now,
			"last_requested_fetch_at":        now,
			"backfill_mode":                  mode,
			"backfill_priority":              priority,
			"recent_pr_repair_cursor_page":   gorm.Expr("CASE WHEN repo_change_sync_states.recent_pr_repair_cursor_page <= 0 THEN 1 ELSE repo_change_sync_states.recent_pr_repair_cursor_page END"),
			"full_history_cursor_page":       gorm.Expr("CASE WHEN repo_change_sync_states.full_history_cursor_page <= 0 THEN 1 ELSE repo_change_sync_states.full_history_cursor_page END"),
			"last_recent_pr_repair_error":    "",
			"last_full_history_repair_error": "",
			"updated_at":                     now,
			"last_error":                     "",
		}),
	}).Create(&state).Error; err != nil {
		return database.RepoChangeSyncState{}, err
	}
	return s.repoChangeStateByRepositoryID(ctx, canonicalRepo.ID)
}

func (s *Service) RequestRecentPRRepair(ctx context.Context, owner, repo string) (database.RepoChangeSyncState, error) {
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
		RepositoryID:                  canonicalRepo.ID,
		BackfillMode:                  changeBackfillModeOff,
		LastRecentPRRepairRequestedAt: &now,
		RecentPRRepairCursorPage:      1,
		FullHistoryCursorPage:         1,
	}
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "repository_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"last_recent_pr_repair_requested_at": now,
			"recent_pr_repair_cursor_page":       1,
			"last_recent_pr_repair_error":        "",
			"updated_at":                         now,
			"last_error":                         "",
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
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		state := database.RepoChangeSyncState{
			RepositoryID:           repositoryID,
			LastWebhookAt:          &seenAt,
			BackfillMode:           changeBackfillModeOff,
			TargetedRefreshPending: true,
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "repository_id"}},
			DoUpdates: clause.Assignments(map[string]any{
				"last_webhook_at":          seenAt,
				"targeted_refresh_pending": true,
				"updated_at":               seenAt,
			}),
		}).Create(&state).Error; err != nil {
			return err
		}

		row := database.RepoTargetedPullRefresh{
			RepositoryID:      repositoryID,
			PullRequestNumber: number,
			RequestedAt:       &seenAt,
			LastWebhookAt:     &seenAt,
		}
		return tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "repository_id"}, {Name: "pull_request_number"}},
			DoUpdates: clause.Assignments(map[string]any{
				"requested_at":    seenAt,
				"last_webhook_at": seenAt,
				"updated_at":      seenAt,
				"last_error":      "",
				"attempt_count":   0,
				"next_attempt_at": nil,
				"parked_at":       nil,
			}),
		}).Create(&row).Error
	})
}

func (s *Service) GetRepoChangeStatus(ctx context.Context, owner, repo string) (gitindex.RepoStatus, error) {
	row, err := s.repoChangeStatusRow(ctx, owner, repo)
	if err != nil {
		return gitindex.RepoStatus{}, err
	}

	now := time.Now().UTC()
	currentPhase, currentPhaseStartedAt := currentRepoPhase(now, row)
	lastFailedRepairPhase, lastRepairError := latestFailedRepairPhase(row)
	openPRMissing, openPRMissingStale := s.repoOpenPRMissing(row, now)
	return gitindex.RepoStatus{
		RepositoryID:                      row.RepositoryID,
		FullName:                          row.FullName,
		LastWebhookAt:                     row.LastWebhookAt,
		LastInventoryScanStartedAt:        row.LastFetchStartedAt,
		LastInventoryScanFinishedAt:       row.LastFetchFinishedAt,
		LastInventoryScanSucceededAt:      row.LastSuccessfulFetchAt,
		LastBackfillStartedAt:             row.LastBackfillStartedAt,
		LastBackfillFinishedAt:            row.LastBackfillFinishedAt,
		BackfillMode:                      normalizeBackfillMode(row.BackfillMode),
		BackfillPriority:                  row.BackfillPriority,
		TargetedRefreshPending:            row.TargetedRefreshPending,
		TargetedRefreshRunning:            leaseIsActive(now, row.TargetedRefreshLeaseHeartbeatAt, row.TargetedRefreshLeaseUntil),
		InventoryGenerationCurrent:        row.InventoryGenerationCurrent,
		InventoryGenerationBuilding:       row.InventoryGenerationBuilding,
		InventoryNeedsRefresh:             row.Dirty,
		InventoryLastCommittedAt:          row.InventoryLastCommittedAt,
		InventoryScanRunning:              leaseIsActive(now, row.FetchLeaseHeartbeatAt, row.FetchLeaseUntil),
		BackfillRunning:                   leaseIsActive(now, row.BackfillLeaseHeartbeatAt, row.BackfillLeaseUntil),
		RecentPRRepairPending:             recentPRRepairPending(row),
		RecentPRRepairRunning:             leaseIsActive(now, row.RecentPRRepairLeaseHeartbeatAt, row.RecentPRRepairLeaseUntil),
		LastRecentPRRepairRequestedAt:     row.LastRecentPRRepairRequestedAt,
		LastRecentPRRepairStartedAt:       row.LastRecentPRRepairStartedAt,
		LastRecentPRRepairFinishedAt:      row.LastRecentPRRepairFinishedAt,
		LastSuccessfulRecentPRRepairAt:    row.LastSuccessfulRecentPRRepairAt,
		LastRecentPRRepairError:           row.LastRecentPRRepairError,
		FullHistoryRepairRunning:          leaseIsActive(now, row.FullHistoryRepairLeaseHeartbeatAt, row.FullHistoryRepairLeaseUntil),
		FullHistoryCursorPage:             row.FullHistoryCursorPage,
		LastFullHistoryRepairStartedAt:    row.LastFullHistoryRepairStartedAt,
		LastFullHistoryRepairFinishedAt:   row.LastFullHistoryRepairFinishedAt,
		LastSuccessfulFullHistoryRepairAt: row.LastSuccessfulFullHistoryRepairAt,
		LastFullHistoryRepairError:        row.LastFullHistoryRepairError,
		CurrentPhase:                      currentPhase,
		CurrentPhaseStartedAt:             currentPhaseStartedAt,
		LastSuccessfulRepairPhase:         latestSuccessfulRepairPhase(row),
		LastFailedRepairPhase:             lastFailedRepairPhase,
		LastRepairError:                   lastRepairError,
		BackfillGeneration:                row.BackfillGeneration,
		BackfillCursor:                    row.OpenPRCursorNumber,
		BackfillCursorUpdatedAt:           row.OpenPRCursorUpdatedAt,
		OpenPRTotal:                       row.OpenPRTotal,
		OpenPRCurrent:                     row.OpenPRCurrent,
		OpenPRStale:                       row.OpenPRStale,
		OpenPRMissing:                     openPRMissing,
		OpenPRMissingStale:                openPRMissingStale,
		LastError:                         row.LastError,
	}, nil
}

func (s *Service) repoOpenPRMissing(row repoChangeStatusRow, now time.Time) (*int, bool) {
	if row.Dirty || row.InventoryLastCommittedAt == nil {
		return nil, true
	}
	maxAge := s.openPRInventoryMaxAge
	if maxAge <= 0 {
		maxAge = 6 * time.Hour
	}
	if row.InventoryLastCommittedAt.UTC().Add(maxAge).Before(now) {
		return nil, true
	}
	missing := maxInt(0, row.OpenPRTotal-row.OpenPRCurrent-row.OpenPRStale)
	return &missing, false
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

func (s *Service) syncPullRequestCore(ctx context.Context, owner, repo string, canonicalRepo database.Repository, number int) (gh.PullRequestResponse, error) {
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
	return pull, nil
}

func (s *Service) repairIssueObject(ctx context.Context, owner, repo string, repositoryID uint, number int) error {
	repair := s.withoutSearch()
	issue, err := repair.github.GetIssue(ctx, owner, repo, number)
	if err != nil {
		return err
	}
	_, err = repair.upsertIssue(ctx, repositoryID, issue)
	return err
}

func (s *Service) repairPullRequestObject(ctx context.Context, owner, repo string, repositoryID uint, number int) error {
	repair := s.withoutSearch()
	pull, err := repair.github.GetPullRequest(ctx, owner, repo, number)
	if err != nil {
		return err
	}
	return repair.upsertPullRequest(ctx, repositoryID, pull)
}

func (s *Service) syncPullRequestChangeOnly(ctx context.Context, owner, repo string, canonicalRepo database.Repository, number int) (gh.PullRequestResponse, error) {
	pull, err := s.syncPullRequestCore(ctx, owner, repo, canonicalRepo, number)
	if err != nil {
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

func (w *ChangeSyncWorker) nextRepoWork(ctx context.Context) (changeSyncRepoWorkKind, error) {
	if _, ok, err := w.pickRecentPRRepairState(ctx); err != nil {
		return changeSyncRepoWorkNone, err
	} else if ok {
		return changeSyncRepoWorkRecentRepair, nil
	}
	if _, ok, err := w.pickFullHistoryRepairState(ctx, 0); err != nil {
		return changeSyncRepoWorkNone, err
	} else if ok {
		return changeSyncRepoWorkFullHistoryRepair, nil
	}
	if _, ok, err := w.pickInventoryScanState(ctx, false); err != nil {
		return changeSyncRepoWorkNone, err
	} else if ok {
		return changeSyncRepoWorkInitialInventory, nil
	}
	if _, ok, err := w.pickBackfillState(ctx); err != nil {
		return changeSyncRepoWorkNone, err
	} else if ok {
		return changeSyncRepoWorkBackfill, nil
	}
	if _, ok, err := w.pickInventoryScanState(ctx, true); err != nil {
		return changeSyncRepoWorkNone, err
	} else if ok {
		return changeSyncRepoWorkAgedInventory, nil
	}
	return changeSyncRepoWorkNone, nil
}

func (w *ChangeSyncWorker) runRepoWork(ctx context.Context, kind changeSyncRepoWorkKind) (bool, error) {
	switch kind {
	case changeSyncRepoWorkRecentRepair:
		return w.processRecentPRRepair(ctx)
	case changeSyncRepoWorkFullHistoryRepair:
		return w.processFullHistoryRepair(ctx)
	case changeSyncRepoWorkInitialInventory:
		return w.processInventoryScan(ctx, false)
	case changeSyncRepoWorkBackfill:
		return w.processBackfillRepo(ctx)
	case changeSyncRepoWorkAgedInventory:
		return w.processInventoryScan(ctx, true)
	default:
		return false, nil
	}
}

func (w *ChangeSyncWorker) pickRecentPRRepairState(ctx context.Context) (database.RepoChangeSyncState, bool, error) {
	now := time.Now().UTC()
	recentAvailableSQL, recentAvailableArgs := w.leases.reclaimableSQL(recentPRRepairLeaseKind, now)
	backfillAvailableSQL, backfillAvailableArgs := w.leases.reclaimableSQL(backfillLeaseKind, now)
	fetchAvailableSQL, fetchAvailableArgs := w.leases.reclaimableSQL(fetchLeaseKind, now)

	var state database.RepoChangeSyncState
	err := w.db.WithContext(ctx).
		Where(recentAvailableSQL, recentAvailableArgs...).
		Where(backfillAvailableSQL, backfillAvailableArgs...).
		Where(fetchAvailableSQL, fetchAvailableArgs...).
		Where(
			"(last_recent_pr_repair_requested_at IS NOT NULL AND (last_recent_pr_repair_finished_at IS NULL OR last_recent_pr_repair_requested_at >= last_recent_pr_repair_finished_at)) OR "+
				"(backfill_mode IN ? AND (last_successful_recent_pr_repair_at IS NULL OR last_successful_recent_pr_repair_at <= ?))",
			[]string{changeBackfillModeOpenAndRecent, changeBackfillModeFullHistory},
			now.Add(-w.recentPRRepairInterval),
		).
		Order("last_recent_pr_repair_requested_at DESC NULLS LAST, backfill_priority DESC, last_successful_recent_pr_repair_at ASC NULLS FIRST, repository_id ASC").
		First(&state).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return database.RepoChangeSyncState{}, false, nil
		}
		return database.RepoChangeSyncState{}, false, err
	}
	return state, true, nil
}

func (w *ChangeSyncWorker) processRecentPRRepair(ctx context.Context) (bool, error) {
	state, ok, err := w.pickRecentPRRepairState(ctx)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}

	now := time.Now().UTC()
	leaseAcquireStartedAt := time.Now()
	acquired, leasedUntil, err := w.leases.acquire(ctx, state.ID, recentPRRepairLeaseKind, now)
	if err != nil {
		return false, err
	}
	if !acquired {
		return false, nil
	}
	startUpdates := map[string]any{
		"last_recent_pr_repair_started_at": now,
		"updated_at":                       now,
	}
	if !recentPRRepairPendingState(state) {
		startUpdates["last_recent_pr_repair_requested_at"] = now
		startUpdates["recent_pr_repair_cursor_page"] = 1
		state.LastRecentPRRepairRequestedAt = &now
		state.RecentPRRepairCursorPage = 1
	}
	if err := w.db.WithContext(ctx).Model(&database.RepoChangeSyncState{}).
		Where("id = ? AND recent_pr_repair_lease_owner_id = ?", state.ID, w.leases.owner()).
		Updates(startUpdates).Error; err != nil {
		return false, err
	}
	slog.Info("recent PR repair lease acquired", "state_id", state.ID, "repository_id", state.RepositoryID, "owner_id", w.leases.owner(), "lease_until", leasedUntil)
	state.RecentPRRepairLeaseOwnerID = w.leases.owner()
	state.RecentPRRepairLeaseStartedAt = &now
	state.RecentPRRepairLeaseHeartbeatAt = &now
	state.RecentPRRepairLeaseUntil = leasedUntil
	result, err := w.runRecentPRRepairPass(ctx, state, time.Since(leaseAcquireStartedAt))
	if err != nil {
		return true, err
	}
	if state.BackfillMode == changeBackfillModeFullHistory && !result.Completed {
		if processed, err := w.processFullHistoryRepairForRepository(ctx, state.RepositoryID); err != nil {
			return true, err
		} else if processed {
			return true, nil
		}
	}
	return true, nil
}

func (w *ChangeSyncWorker) processFullHistoryRepair(ctx context.Context) (bool, error) {
	return w.processFullHistoryRepairForRepository(ctx, 0)
}

func (w *ChangeSyncWorker) pickFullHistoryRepairState(ctx context.Context, repositoryID uint) (database.RepoChangeSyncState, bool, error) {
	now := time.Now().UTC()
	fullHistoryAvailableSQL, fullHistoryAvailableArgs := w.leases.reclaimableSQL(fullHistoryRepairLeaseKind, now)
	recentAvailableSQL, recentAvailableArgs := w.leases.reclaimableSQL(recentPRRepairLeaseKind, now)
	backfillAvailableSQL, backfillAvailableArgs := w.leases.reclaimableSQL(backfillLeaseKind, now)
	fetchAvailableSQL, fetchAvailableArgs := w.leases.reclaimableSQL(fetchLeaseKind, now)

	query := w.db.WithContext(ctx).
		Where("backfill_mode = ?", changeBackfillModeFullHistory).
		Where(fullHistoryAvailableSQL, fullHistoryAvailableArgs...).
		Where(recentAvailableSQL, recentAvailableArgs...).
		Where(backfillAvailableSQL, backfillAvailableArgs...).
		Where(fetchAvailableSQL, fetchAvailableArgs...).
		Where("(last_successful_full_history_repair_at IS NULL OR last_successful_full_history_repair_at <= ?)", now.Add(-w.fullHistoryRepairInterval))
	if repositoryID != 0 {
		query = query.Where("repository_id = ?", repositoryID)
	}

	var state database.RepoChangeSyncState
	err := query.Order("backfill_priority DESC, last_successful_full_history_repair_at ASC NULLS FIRST, repository_id ASC").
		First(&state).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return database.RepoChangeSyncState{}, false, nil
		}
		return database.RepoChangeSyncState{}, false, err
	}
	return state, true, nil
}

func (w *ChangeSyncWorker) processFullHistoryRepairForRepository(ctx context.Context, repositoryID uint) (bool, error) {
	state, ok, err := w.pickFullHistoryRepairState(ctx, repositoryID)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}

	now := time.Now().UTC()
	leaseAcquireStartedAt := time.Now()
	acquired, leasedUntil, err := w.leases.acquire(ctx, state.ID, fullHistoryRepairLeaseKind, now)
	if err != nil {
		return false, err
	}
	if !acquired {
		return false, nil
	}
	if err := w.db.WithContext(ctx).Model(&database.RepoChangeSyncState{}).
		Where("id = ? AND full_history_repair_lease_owner_id = ?", state.ID, w.leases.owner()).
		Updates(map[string]any{
			"last_full_history_repair_started_at": now,
			"updated_at":                          now,
		}).Error; err != nil {
		return false, err
	}
	slog.Info("full history repair lease acquired", "state_id", state.ID, "repository_id", state.RepositoryID, "owner_id", w.leases.owner(), "lease_until", leasedUntil)
	state.FullHistoryRepairLeaseOwnerID = w.leases.owner()
	state.FullHistoryRepairLeaseStartedAt = &now
	state.FullHistoryRepairLeaseHeartbeatAt = &now
	state.FullHistoryRepairLeaseUntil = leasedUntil
	return true, w.runFullHistoryRepairPass(ctx, state, time.Since(leaseAcquireStartedAt))
}

func (w *ChangeSyncWorker) pickInventoryScanState(ctx context.Context, ageOnly bool) (database.RepoChangeSyncState, bool, error) {
	now := time.Now().UTC()
	fetchAvailableSQL, fetchAvailableArgs := w.leases.reclaimableSQL(fetchLeaseKind, now)
	backfillAvailableSQL, backfillAvailableArgs := w.leases.reclaimableSQL(backfillLeaseKind, now)
	query := w.db.WithContext(ctx).
		Where("backfill_mode <> ?", changeBackfillModeOff).
		Where(fetchAvailableSQL, fetchAvailableArgs...).
		Where(backfillAvailableSQL, backfillAvailableArgs...)
	if ageOnly {
		query = query.
			Where("inventory_generation_current <> 0").
			Where("inventory_last_committed_at IS NOT NULL AND inventory_last_committed_at <= ?", now.Add(-w.openPRInventoryMaxAge)).
			Order("backfill_priority DESC, inventory_last_committed_at ASC, repository_id ASC")
	} else {
		query = query.
			Where("(inventory_generation_current = 0 OR inventory_last_committed_at IS NULL)").
			Order("CASE WHEN inventory_generation_current = 0 THEN 0 ELSE 1 END ASC, backfill_priority DESC, inventory_last_committed_at ASC NULLS FIRST, repository_id ASC")
	}

	var state database.RepoChangeSyncState
	err := query.First(&state).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return database.RepoChangeSyncState{}, false, nil
		}
		return database.RepoChangeSyncState{}, false, err
	}
	return state, true, nil
}

func (w *ChangeSyncWorker) processInventoryScan(ctx context.Context, ageOnly bool) (bool, error) {
	state, ok, err := w.pickInventoryScanState(ctx, ageOnly)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}

	now := time.Now().UTC()
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

func (w *ChangeSyncWorker) pickBackfillState(ctx context.Context) (database.RepoChangeSyncState, bool, error) {
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
			return database.RepoChangeSyncState{}, false, nil
		}
		return database.RepoChangeSyncState{}, false, err
	}
	return state, true, nil
}

func (w *ChangeSyncWorker) processBackfillRepo(ctx context.Context) (bool, error) {
	state, ok, err := w.pickBackfillState(ctx)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}

	now := time.Now().UTC()
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

func (w *ChangeSyncWorker) runRecentPRRepairPass(ctx context.Context, state database.RepoChangeSyncState, leaseWait time.Duration) (repairPassMetrics, error) {
	var (
		result     repairPassMetrics
		repository database.Repository
	)
	startedAt := time.Now().UTC()
	runErr := w.runWithLeaseHeartbeat(ctx, state.ID, recentPRRepairLeaseKind, func(passCtx context.Context) error {
		var err error
		repository, err = repositoryByID(passCtx, w.db, state.RepositoryID)
		if err != nil {
			return err
		}
		owner, name, err := splitFullName(repository.FullName)
		if err != nil {
			return err
		}
		result, err = w.service.RepairRecentPullRequests(
			passCtx,
			owner,
			name,
			time.Now().UTC().Add(-w.recentPRRepairWindow),
			state.RecentPRRepairCursorPage,
			w.recentPRRepairMaxPages,
			w.recentPRRepairPerPage,
		)
		return err
	})
	if runErr != nil {
		if w.service.repairMetrics != nil {
			w.service.repairMetrics.recordFailure(recentRepairPhase, state.RepositoryID, repository.FullName, leaseWait, time.Since(startedAt), runErr)
		}
		return repairPassMetrics{}, w.finishRecentPRRepairWithError(ctx, state, runErr)
	}
	if w.service.repairMetrics != nil {
		w.service.repairMetrics.recordSuccess(recentRepairPhase, state.RepositoryID, repository.FullName, result, leaseWait, time.Since(startedAt))
	}
	return result, w.completeRecentPRRepairPass(ctx, state, result)
}

func (w *ChangeSyncWorker) runFullHistoryRepairPass(ctx context.Context, state database.RepoChangeSyncState, leaseWait time.Duration) error {
	var (
		repository database.Repository
		result     repairPassMetrics
	)
	startedAt := time.Now().UTC()
	runErr := w.runWithLeaseHeartbeat(ctx, state.ID, fullHistoryRepairLeaseKind, func(passCtx context.Context) error {
		var err error
		repository, err = repositoryByID(passCtx, w.db, state.RepositoryID)
		if err != nil {
			return err
		}
		owner, name, err := splitFullName(repository.FullName)
		if err != nil {
			return err
		}
		page := state.FullHistoryCursorPage
		if page <= 0 {
			page = 1
		}
		for i := 0; i < w.fullHistoryRepairMaxPages; i++ {
			pageResult, err := w.service.RepairPullRequestHistoryPage(passCtx, owner, name, page, w.fullHistoryRepairPerPage)
			if err != nil {
				return err
			}
			accumulateRepairPassMetrics(&result, pageResult)
			if pageResult.Completed {
				result.Completed = true
				result.NextPage = 1
				break
			}
			page++
			result.NextPage = page
		}
		state.FullHistoryCursorPage = page
		return nil
	})
	if runErr != nil {
		if w.service.repairMetrics != nil {
			w.service.repairMetrics.recordFailure(fullHistoryRepairPhase, state.RepositoryID, repository.FullName, leaseWait, time.Since(startedAt), runErr)
		}
		return w.finishFullHistoryRepairWithError(ctx, state, runErr)
	}
	if w.service.repairMetrics != nil {
		w.service.repairMetrics.recordSuccess(fullHistoryRepairPhase, state.RepositoryID, repository.FullName, result, leaseWait, time.Since(startedAt))
	}
	return w.completeFullHistoryRepairPass(ctx, state, result)
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

func (w *ChangeSyncWorker) completeRecentPRRepairPass(ctx context.Context, state database.RepoChangeSyncState, result repairPassMetrics) error {
	now := time.Now().UTC()
	updates := map[string]any{
		"last_error":                        "",
		"last_recent_pr_repair_error":       "",
		"last_recent_pr_repair_finished_at": now,
	}
	if result.Completed {
		updates["last_recent_pr_repair_requested_at"] = nil
		updates["last_successful_recent_pr_repair_at"] = now
		updates["recent_pr_repair_cursor_page"] = 1
	} else {
		nextPage := result.NextPage
		if nextPage <= 1 {
			nextPage = maxInt(2, state.RecentPRRepairCursorPage+1)
		}
		updates["last_recent_pr_repair_requested_at"] = now
		updates["recent_pr_repair_cursor_page"] = nextPage
	}
	if err := w.leases.release(ctx, state.ID, recentPRRepairLeaseKind, updates); err != nil {
		return err
	}
	slog.Info(
		"recent PR repair pass completed",
		"state_id", state.ID,
		"repository_id", state.RepositoryID,
		"owner_id", w.leases.owner(),
		"pulls_scanned", result.PullsScanned,
		"issues_scanned", result.IssuesScanned,
		"pulls_stale", result.PullsStale,
		"issues_stale", result.IssuesStale,
		"pulls_unchanged", result.PullsUnchanged,
		"issues_unchanged", result.IssuesUnchanged,
		"pull_fetches", result.PullFetches,
		"issue_fetches", result.IssueFetches,
		"pulls_repaired", result.PullsRepaired,
		"issues_repaired", result.IssuesRepaired,
		"apply_writes", result.ApplyWrites,
		"completed", result.Completed,
		"next_page", updates["recent_pr_repair_cursor_page"],
	)
	return nil
}

func (w *ChangeSyncWorker) completeFullHistoryRepairPass(ctx context.Context, state database.RepoChangeSyncState, result repairPassMetrics) error {
	now := time.Now().UTC()
	nextPage := state.FullHistoryCursorPage
	if result.Completed || nextPage <= 0 {
		nextPage = 1
	}
	updates := map[string]any{
		"last_error":                             "",
		"last_full_history_repair_error":         "",
		"last_full_history_repair_finished_at":   now,
		"last_successful_full_history_repair_at": now,
		"full_history_cursor_page":               nextPage,
	}
	if err := w.leases.release(ctx, state.ID, fullHistoryRepairLeaseKind, updates); err != nil {
		return err
	}
	slog.Info(
		"full history repair pass completed",
		"state_id", state.ID,
		"repository_id", state.RepositoryID,
		"owner_id", w.leases.owner(),
		"pulls_scanned", result.PullsScanned,
		"issues_scanned", result.IssuesScanned,
		"pulls_stale", result.PullsStale,
		"issues_stale", result.IssuesStale,
		"pulls_unchanged", result.PullsUnchanged,
		"issues_unchanged", result.IssuesUnchanged,
		"pull_fetches", result.PullFetches,
		"issue_fetches", result.IssueFetches,
		"pulls_repaired", result.PullsRepaired,
		"issues_repaired", result.IssuesRepaired,
		"apply_writes", result.ApplyWrites,
		"completed", result.Completed,
		"next_page", nextPage,
	)
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

func (w *ChangeSyncWorker) finishRecentPRRepairWithError(ctx context.Context, state database.RepoChangeSyncState, runErr error) error {
	now := time.Now().UTC()
	updates := map[string]any{
		"last_error":                        runErr.Error(),
		"last_recent_pr_repair_error":       runErr.Error(),
		"last_recent_pr_repair_finished_at": now,
	}
	if err := w.leases.release(ctx, state.ID, recentPRRepairLeaseKind, updates); err != nil {
		return err
	}
	slog.Warn("recent PR repair pass failed", "state_id", state.ID, "repository_id", state.RepositoryID, "owner_id", w.leases.owner(), "error", runErr)
	return runErr
}

func (w *ChangeSyncWorker) finishFullHistoryRepairWithError(ctx context.Context, state database.RepoChangeSyncState, runErr error) error {
	now := time.Now().UTC()
	updates := map[string]any{
		"last_error":                           runErr.Error(),
		"last_full_history_repair_error":       runErr.Error(),
		"last_full_history_repair_finished_at": now,
	}
	if err := w.leases.release(ctx, state.ID, fullHistoryRepairLeaseKind, updates); err != nil {
		return err
	}
	slog.Warn("full history repair pass failed", "state_id", state.ID, "repository_id", state.RepositoryID, "owner_id", w.leases.owner(), "error", runErr)
	return runErr
}

func (w *ChangeSyncWorker) acquireNextTargetedRefresh(ctx context.Context) (database.RepoTargetedPullRefresh, bool, error) {
	now := time.Now().UTC()
	staleBefore := now.Add(-w.leases.staleAfter)
	recentDueBefore := now.Add(-w.recentPRRepairInterval)
	fullHistoryDueBefore := now.Add(-w.fullHistoryRepairInterval)
	var row database.RepoTargetedPullRefresh
	err := w.db.WithContext(ctx).
		Joins("LEFT JOIN repo_change_sync_states ON repo_change_sync_states.repository_id = repo_targeted_pull_refreshes.repository_id").
		Where("requested_at IS NOT NULL").
		Where("(last_completed_at IS NULL OR requested_at > last_completed_at)").
		Where("parked_at IS NULL").
		Where("(attempt_count = 0 OR next_attempt_at IS NULL OR next_attempt_at <= ?)", now).
		Where("(lease_until IS NULL OR lease_until <= ? OR lease_heartbeat_at IS NULL OR lease_heartbeat_at <= ?)", now, staleBefore).
		Where(
			`repo_change_sync_states.repository_id IS NULL OR NOT (
				((repo_change_sync_states.last_recent_pr_repair_requested_at IS NOT NULL AND
					(repo_change_sync_states.last_recent_pr_repair_finished_at IS NULL OR repo_change_sync_states.last_recent_pr_repair_requested_at >= repo_change_sync_states.last_recent_pr_repair_finished_at)) OR
				 (repo_change_sync_states.backfill_mode IN ? AND
					(repo_change_sync_states.last_successful_recent_pr_repair_at IS NULL OR repo_change_sync_states.last_successful_recent_pr_repair_at <= ?)))
				OR
				(repo_change_sync_states.backfill_mode = ? AND
					(repo_change_sync_states.last_successful_full_history_repair_at IS NULL OR repo_change_sync_states.last_successful_full_history_repair_at <= ?))
			)`,
			[]string{changeBackfillModeOpenAndRecent, changeBackfillModeFullHistory},
			recentDueBefore,
			changeBackfillModeFullHistory,
			fullHistoryDueBefore,
		).
		Order("CASE WHEN repo_targeted_pull_refreshes.attempt_count = 0 THEN 0 ELSE 1 END ASC").
		Order("CASE WHEN repo_targeted_pull_refreshes.attempt_count = 0 THEN repo_targeted_pull_refreshes.requested_at END DESC").
		Order("CASE WHEN repo_targeted_pull_refreshes.attempt_count > 0 THEN repo_targeted_pull_refreshes.next_attempt_at END ASC").
		Order("CASE WHEN repo_targeted_pull_refreshes.attempt_count > 0 THEN repo_targeted_pull_refreshes.requested_at END DESC").
		Order("repo_targeted_pull_refreshes.repository_id ASC, repo_targeted_pull_refreshes.pull_request_number ASC").
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
	if err := w.db.WithContext(ctx).Model(&database.RepoChangeSyncState{}).
		Where("repository_id = ?", row.RepositoryID).
		Updates(map[string]any{
			"targeted_refresh_pending":            true,
			"targeted_refresh_lease_heartbeat_at": now,
			"targeted_refresh_lease_until":        leaseUntil,
			"updated_at":                          now,
		}).Error; err != nil {
		return database.RepoTargetedPullRefresh{}, false, err
	}
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
		updates["attempt_count"] = 0
		updates["next_attempt_at"] = nil
		updates["parked_at"] = nil
	} else {
		updates["last_error"] = refreshErr.Error()
		attemptCount := row.AttemptCount + 1
		updates["attempt_count"] = attemptCount
		if attemptCount >= 5 {
			updates["parked_at"] = now
			updates["next_attempt_at"] = nil
		} else {
			nextAttemptAt := now.Add(targetedRefreshBackoff(attemptCount))
			updates["next_attempt_at"] = nextAttemptAt
			updates["parked_at"] = nil
		}
	}
	if err := w.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&database.RepoTargetedPullRefresh{}).
			Where("id = ? AND lease_owner_id = ?", row.ID, w.leases.owner()).
			Updates(updates).Error; err != nil {
			return err
		}
		pending, err := targetedRefreshPendingExists(ctx, tx, row.RepositoryID)
		if err != nil {
			return err
		}
		stateUpdates := map[string]any{
			"targeted_refresh_pending":            pending,
			"targeted_refresh_lease_heartbeat_at": nil,
			"targeted_refresh_lease_until":        nil,
			"updated_at":                          now,
		}
		if refreshErr == nil {
			stateUpdates["last_error"] = ""
		} else {
			stateUpdates["last_error"] = refreshErr.Error()
		}
		return tx.Model(&database.RepoChangeSyncState{}).
			Where("repository_id = ?", row.RepositoryID).
			Updates(stateUpdates).Error
	}); err != nil {
		return err
	}
	return refreshErr
}

func targetedRefreshPendingExists(ctx context.Context, db *gorm.DB, repositoryID uint) (bool, error) {
	var pending sql.NullBool
	if err := db.WithContext(ctx).Raw(
		`SELECT EXISTS (
			SELECT 1
			FROM repo_targeted_pull_refreshes
			WHERE repository_id = ?
			  AND requested_at IS NOT NULL
			  AND (last_completed_at IS NULL OR requested_at > last_completed_at)
			  AND parked_at IS NULL
		)`,
		repositoryID,
	).Scan(&pending).Error; err != nil {
		return false, err
	}
	return pending.Bool, nil
}

func targetedRefreshBackoff(attemptCount int) time.Duration {
	switch attemptCount {
	case 1:
		return time.Minute
	case 2:
		return 5 * time.Minute
	case 3:
		return 15 * time.Minute
	default:
		return time.Hour
	}
}

type repoChangeStatusRow struct {
	RepositoryID                      uint       `gorm:"column:repository_id"`
	FullName                          string     `gorm:"column:full_name"`
	Dirty                             bool       `gorm:"column:dirty"`
	LastWebhookAt                     *time.Time `gorm:"column:last_webhook_at"`
	LastFetchStartedAt                *time.Time `gorm:"column:last_fetch_started_at"`
	LastFetchFinishedAt               *time.Time `gorm:"column:last_fetch_finished_at"`
	LastSuccessfulFetchAt             *time.Time `gorm:"column:last_successful_fetch_at"`
	LastBackfillStartedAt             *time.Time `gorm:"column:last_backfill_started_at"`
	LastBackfillFinishedAt            *time.Time `gorm:"column:last_backfill_finished_at"`
	LastRecentPRRepairRequestedAt     *time.Time `gorm:"column:last_recent_pr_repair_requested_at"`
	LastRecentPRRepairStartedAt       *time.Time `gorm:"column:last_recent_pr_repair_started_at"`
	LastRecentPRRepairFinishedAt      *time.Time `gorm:"column:last_recent_pr_repair_finished_at"`
	LastSuccessfulRecentPRRepairAt    *time.Time `gorm:"column:last_successful_recent_pr_repair_at"`
	LastRecentPRRepairError           string     `gorm:"column:last_recent_pr_repair_error"`
	RecentPRRepairCursorPage          int        `gorm:"column:recent_pr_repair_cursor_page"`
	LastFullHistoryRepairStartedAt    *time.Time `gorm:"column:last_full_history_repair_started_at"`
	LastFullHistoryRepairFinishedAt   *time.Time `gorm:"column:last_full_history_repair_finished_at"`
	LastSuccessfulFullHistoryRepairAt *time.Time `gorm:"column:last_successful_full_history_repair_at"`
	LastFullHistoryRepairError        string     `gorm:"column:last_full_history_repair_error"`
	InventoryGenerationCurrent        int        `gorm:"column:inventory_generation_current"`
	InventoryGenerationBuilding       *int       `gorm:"column:inventory_generation_building"`
	InventoryLastCommittedAt          *time.Time `gorm:"column:inventory_last_committed_at"`
	OpenPRTotal                       int        `gorm:"column:open_pr_total"`
	OpenPRCurrent                     int        `gorm:"column:open_pr_current"`
	OpenPRStale                       int        `gorm:"column:open_pr_stale"`
	BackfillGeneration                int        `gorm:"column:backfill_generation"`
	OpenPRCursorNumber                *int       `gorm:"column:open_pr_cursor_number"`
	OpenPRCursorUpdatedAt             *time.Time `gorm:"column:open_pr_cursor_updated_at"`
	BackfillMode                      string     `gorm:"column:backfill_mode"`
	BackfillPriority                  int        `gorm:"column:backfill_priority"`
	FullHistoryCursorPage             int        `gorm:"column:full_history_cursor_page"`
	FetchLeaseHeartbeatAt             *time.Time `gorm:"column:fetch_lease_heartbeat_at"`
	FetchLeaseUntil                   *time.Time `gorm:"column:fetch_lease_until"`
	BackfillLeaseHeartbeatAt          *time.Time `gorm:"column:backfill_lease_heartbeat_at"`
	BackfillLeaseUntil                *time.Time `gorm:"column:backfill_lease_until"`
	RecentPRRepairLeaseHeartbeatAt    *time.Time `gorm:"column:recent_pr_repair_lease_heartbeat_at"`
	RecentPRRepairLeaseUntil          *time.Time `gorm:"column:recent_pr_repair_lease_until"`
	FullHistoryRepairLeaseHeartbeatAt *time.Time `gorm:"column:full_history_repair_lease_heartbeat_at"`
	FullHistoryRepairLeaseUntil       *time.Time `gorm:"column:full_history_repair_lease_until"`
	TargetedRefreshPending            bool       `gorm:"column:targeted_refresh_pending"`
	TargetedRefreshLeaseHeartbeatAt   *time.Time `gorm:"column:targeted_refresh_lease_heartbeat_at"`
	TargetedRefreshLeaseUntil         *time.Time `gorm:"column:targeted_refresh_lease_until"`
	LastError                         string     `gorm:"column:last_error"`
}

func (s *Service) repoChangeStatusRow(ctx context.Context, owner, repo string) (repoChangeStatusRow, error) {
	fullName := strings.TrimSpace(owner) + "/" + strings.TrimSpace(repo)
	var row repoChangeStatusRow
	result := s.db.WithContext(ctx).
		Table("repositories").
		Select(`
			repositories.id AS repository_id,
			repositories.full_name AS full_name,
			COALESCE(repo_change_sync_states.dirty, FALSE) AS dirty,
			repo_change_sync_states.last_webhook_at AS last_webhook_at,
			repo_change_sync_states.last_fetch_started_at AS last_fetch_started_at,
			repo_change_sync_states.last_fetch_finished_at AS last_fetch_finished_at,
			repo_change_sync_states.last_successful_fetch_at AS last_successful_fetch_at,
			repo_change_sync_states.last_backfill_started_at AS last_backfill_started_at,
			repo_change_sync_states.last_backfill_finished_at AS last_backfill_finished_at,
			repo_change_sync_states.last_recent_pr_repair_requested_at AS last_recent_pr_repair_requested_at,
			repo_change_sync_states.last_recent_pr_repair_started_at AS last_recent_pr_repair_started_at,
			repo_change_sync_states.last_recent_pr_repair_finished_at AS last_recent_pr_repair_finished_at,
			repo_change_sync_states.last_successful_recent_pr_repair_at AS last_successful_recent_pr_repair_at,
			COALESCE(repo_change_sync_states.last_recent_pr_repair_error, '') AS last_recent_pr_repair_error,
			COALESCE(repo_change_sync_states.recent_pr_repair_cursor_page, 1) AS recent_pr_repair_cursor_page,
			repo_change_sync_states.last_full_history_repair_started_at AS last_full_history_repair_started_at,
			repo_change_sync_states.last_full_history_repair_finished_at AS last_full_history_repair_finished_at,
			repo_change_sync_states.last_successful_full_history_repair_at AS last_successful_full_history_repair_at,
			COALESCE(repo_change_sync_states.last_full_history_repair_error, '') AS last_full_history_repair_error,
			COALESCE(repo_change_sync_states.inventory_generation_current, 0) AS inventory_generation_current,
			repo_change_sync_states.inventory_generation_building AS inventory_generation_building,
			repo_change_sync_states.inventory_last_committed_at AS inventory_last_committed_at,
			COALESCE(repo_change_sync_states.open_pr_total, 0) AS open_pr_total,
			COALESCE(repo_change_sync_states.open_pr_current, 0) AS open_pr_current,
			COALESCE(repo_change_sync_states.open_pr_stale, 0) AS open_pr_stale,
			COALESCE(repo_change_sync_states.backfill_generation, 0) AS backfill_generation,
			repo_change_sync_states.open_pr_cursor_number AS open_pr_cursor_number,
			repo_change_sync_states.open_pr_cursor_updated_at AS open_pr_cursor_updated_at,
			COALESCE(repo_change_sync_states.backfill_mode, '') AS backfill_mode,
			COALESCE(repo_change_sync_states.backfill_priority, 0) AS backfill_priority,
			COALESCE(repo_change_sync_states.full_history_cursor_page, 1) AS full_history_cursor_page,
			repo_change_sync_states.fetch_lease_heartbeat_at AS fetch_lease_heartbeat_at,
			repo_change_sync_states.fetch_lease_until AS fetch_lease_until,
			repo_change_sync_states.backfill_lease_heartbeat_at AS backfill_lease_heartbeat_at,
			repo_change_sync_states.backfill_lease_until AS backfill_lease_until,
			repo_change_sync_states.recent_pr_repair_lease_heartbeat_at AS recent_pr_repair_lease_heartbeat_at,
			repo_change_sync_states.recent_pr_repair_lease_until AS recent_pr_repair_lease_until,
			repo_change_sync_states.full_history_repair_lease_heartbeat_at AS full_history_repair_lease_heartbeat_at,
			repo_change_sync_states.full_history_repair_lease_until AS full_history_repair_lease_until,
			COALESCE(repo_change_sync_states.targeted_refresh_pending, FALSE) AS targeted_refresh_pending,
			repo_change_sync_states.targeted_refresh_lease_heartbeat_at AS targeted_refresh_lease_heartbeat_at,
			repo_change_sync_states.targeted_refresh_lease_until AS targeted_refresh_lease_until,
			COALESCE(repo_change_sync_states.last_error, '') AS last_error
		`).
		Joins("LEFT JOIN repo_change_sync_states ON repo_change_sync_states.repository_id = repositories.id").
		Where("repositories.full_name = ?", fullName).
		Take(&row)
	if result.Error != nil {
		return repoChangeStatusRow{}, result.Error
	}
	return row, nil
}

func recentPRRepairPending(row repoChangeStatusRow) bool {
	if row.LastRecentPRRepairRequestedAt == nil {
		return false
	}
	if row.LastRecentPRRepairFinishedAt == nil {
		return true
	}
	return !row.LastRecentPRRepairRequestedAt.Before(row.LastRecentPRRepairFinishedAt.UTC())
}

func recentPRRepairPendingState(state database.RepoChangeSyncState) bool {
	if state.LastRecentPRRepairRequestedAt == nil {
		return false
	}
	if state.LastRecentPRRepairFinishedAt == nil {
		return true
	}
	return !state.LastRecentPRRepairRequestedAt.Before(state.LastRecentPRRepairFinishedAt.UTC())
}

func currentRepoPhase(now time.Time, row repoChangeStatusRow) (string, *time.Time) {
	switch {
	case leaseIsActive(now, row.RecentPRRepairLeaseHeartbeatAt, row.RecentPRRepairLeaseUntil):
		return string(recentRepairPhase), row.LastRecentPRRepairStartedAt
	case leaseIsActive(now, row.FullHistoryRepairLeaseHeartbeatAt, row.FullHistoryRepairLeaseUntil):
		return string(fullHistoryRepairPhase), row.LastFullHistoryRepairStartedAt
	case leaseIsActive(now, row.FetchLeaseHeartbeatAt, row.FetchLeaseUntil):
		return "inventory_scan", row.LastFetchStartedAt
	case leaseIsActive(now, row.BackfillLeaseHeartbeatAt, row.BackfillLeaseUntil):
		return "backfill", row.LastBackfillStartedAt
	case leaseIsActive(now, row.TargetedRefreshLeaseHeartbeatAt, row.TargetedRefreshLeaseUntil):
		return "targeted_refresh", nil
	default:
		return "", nil
	}
}

func latestSuccessfulRepairPhase(row repoChangeStatusRow) string {
	var (
		phase string
		at    *time.Time
	)
	for _, candidate := range []struct {
		name string
		at   *time.Time
	}{
		{name: string(recentRepairPhase), at: row.LastSuccessfulRecentPRRepairAt},
		{name: string(fullHistoryRepairPhase), at: row.LastSuccessfulFullHistoryRepairAt},
	} {
		if candidate.at == nil {
			continue
		}
		if at == nil || candidate.at.After(at.UTC()) {
			at = candidate.at
			phase = candidate.name
		}
	}
	return phase
}

func latestFailedRepairPhase(row repoChangeStatusRow) (string, string) {
	var (
		phase string
		err   string
		at    *time.Time
	)
	for _, candidate := range []struct {
		name string
		err  string
		at   *time.Time
	}{
		{name: string(recentRepairPhase), err: row.LastRecentPRRepairError, at: row.LastRecentPRRepairFinishedAt},
		{name: string(fullHistoryRepairPhase), err: row.LastFullHistoryRepairError, at: row.LastFullHistoryRepairFinishedAt},
	} {
		if candidate.err == "" || candidate.at == nil {
			continue
		}
		if at == nil || candidate.at.After(at.UTC()) {
			at = candidate.at
			phase = candidate.name
			err = candidate.err
		}
	}
	return phase, err
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
	case changeBackfillModeOpenAndRecent:
		return changeBackfillModeOpenAndRecent
	case changeBackfillModeFullHistory:
		return changeBackfillModeFullHistory
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

		result.OpenPRCurrent, result.OpenPRStale = adjustBackfillCounts(result.OpenPRCurrent, result.OpenPRStale, "", freshness)
	}

	if err := s.prepareInventoryGeneration(ctx, repositoryID, state.InventoryGenerationCurrent, nextGeneration); err != nil {
		return RepoBackfillResult{}, err
	}
	if err := s.writeInventoryGeneration(ctx, inventoryRows, now); err != nil {
		return RepoBackfillResult{}, err
	}
	if err := s.applySnapshotFreshnessUpdates(ctx, snapshotFreshnessUpdates, now); err != nil {
		return RepoBackfillResult{}, err
	}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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

func (s *Service) prepareInventoryGeneration(ctx context.Context, repositoryID uint, currentGeneration, nextGeneration int) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("repository_id = ? AND generation = ?", repositoryID, nextGeneration).
			Delete(&database.RepoOpenPullInventory{}).Error; err != nil {
			return err
		}

		prune := tx.Where("repository_id = ? AND generation <> ?", repositoryID, currentGeneration)
		if currentGeneration <= 0 {
			prune = tx.Where("repository_id = ?", repositoryID)
		}
		return prune.Where("generation <> ?", nextGeneration).
			Delete(&database.RepoOpenPullInventory{}).Error
	})
}

func (s *Service) writeInventoryGeneration(ctx context.Context, inventoryRows []database.RepoOpenPullInventory, now time.Time) error {
	if len(inventoryRows) == 0 {
		return nil
	}

	for start := 0; start < len(inventoryRows); start += defaultInventoryWriteBatchSize {
		end := start + defaultInventoryWriteBatchSize
		if end > len(inventoryRows) {
			end = len(inventoryRows)
		}
		batch := inventoryRows[start:end]
		if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return tx.Clauses(clause.OnConflict{
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
			}).CreateInBatches(batch, len(batch)).Error
		}); err != nil {
			return err
		}
	}

	return nil
}

func (s *Service) applySnapshotFreshnessUpdates(ctx context.Context, snapshotFreshnessUpdates map[string][]uint, now time.Time) error {
	for freshness, snapshotIDs := range snapshotFreshnessUpdates {
		for start := 0; start < len(snapshotIDs); start += defaultInventoryWriteBatchSize {
			end := start + defaultInventoryWriteBatchSize
			if end > len(snapshotIDs) {
				end = len(snapshotIDs)
			}
			if err := s.db.WithContext(ctx).Model(&database.PullRequestChangeSnapshot{}).
				Where("id IN ?", snapshotIDs[start:end]).
				Updates(map[string]any{
					"index_freshness": freshness,
					"updated_at":      now,
				}).Error; err != nil {
				return err
			}
		}
	}

	return nil
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
		inventoryFound := true
		if err := tx.Where("repository_id = ? AND generation = ? AND pull_request_number = ?", repositoryID, state.InventoryGenerationCurrent, number).
			First(&inventory).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				inventoryFound = false
			} else {
				return err
			}
		}

		var snapshot database.PullRequestChangeSnapshot
		snapshotFound := true
		if err := tx.Where("repository_id = ? AND pull_request_number = ?", repositoryID, number).
			First(&snapshot).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				snapshotFound = false
			} else {
				return err
			}
		}
		var snapshotPtr *database.PullRequestChangeSnapshot
		if snapshotFound {
			snapshotPtr = &snapshot
		}
		newFreshness := desiredFreshness(snapshotPtr, pull)

		if snapshotFound && snapshot.IndexFreshness != newFreshness {
			if err := tx.Model(&database.PullRequestChangeSnapshot{}).
				Where("id = ?", snapshot.ID).
				Updates(map[string]any{
					"index_freshness": newFreshness,
					"updated_at":      now,
				}).Error; err != nil {
				return err
			}
		}

		if strings.TrimSpace(pull.State) != "open" {
			if inventoryFound {
				if err := tx.Where("id = ?", inventory.ID).Delete(&database.RepoOpenPullInventory{}).Error; err != nil {
					return err
				}
			}
			return s.updateRepoInventoryCountsTx(tx, *state, now, nil)
		}

		if inventoryFound {
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
		} else {
			inventoryRow := inventoryFromPull(repositoryID, pull)
			inventoryRow.Generation = state.InventoryGenerationCurrent
			inventoryRow.FreshnessState = newFreshness
			inventoryRow.LastSeenAt = now
			if err := tx.Create(&inventoryRow).Error; err != nil {
				return err
			}
		}
		return s.updateRepoInventoryCountsTx(tx, *state, now, nil)
	})
}

func (s *Service) markInventoryBaseRefStale(ctx context.Context, repositoryID uint, ref string) error {
	baseRef := normalizeBackfillBaseRef(ref)
	if repositoryID == 0 || baseRef == "" {
		return nil
	}

	state, err := s.repoChangeStateOptional(ctx, repositoryID)
	if err != nil {
		return err
	}
	if state == nil || state.InventoryGenerationCurrent <= 0 {
		return nil
	}

	now := time.Now().UTC()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&database.RepoOpenPullInventory{}).
			Where("repository_id = ? AND generation = ? AND base_ref = ?", repositoryID, state.InventoryGenerationCurrent, baseRef).
			Where("freshness_state <> ?", "stale_base_moved").
			Updates(map[string]any{
				"freshness_state": "stale_base_moved",
				"updated_at":      now,
			}).Error; err != nil {
			return err
		}
		return s.updateRepoInventoryCountsTx(tx, *state, now, nil)
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

func (s *Service) updateRepoInventoryCountsTx(tx *gorm.DB, state database.RepoChangeSyncState, now time.Time, extraUpdates map[string]any) error {
	var total int64
	if err := tx.Model(&database.RepoOpenPullInventory{}).
		Where("repository_id = ? AND generation = ?", state.RepositoryID, state.InventoryGenerationCurrent).
		Count(&total).Error; err != nil {
		return err
	}

	var current int64
	if err := tx.Model(&database.RepoOpenPullInventory{}).
		Where("repository_id = ? AND generation = ?", state.RepositoryID, state.InventoryGenerationCurrent).
		Where("freshness_state = ?", "current").
		Count(&current).Error; err != nil {
		return err
	}

	var stale int64
	if err := tx.Model(&database.RepoOpenPullInventory{}).
		Where("repository_id = ? AND generation = ?", state.RepositoryID, state.InventoryGenerationCurrent).
		Where("freshness_state <> '' AND freshness_state <> ?", "current").
		Count(&stale).Error; err != nil {
		return err
	}

	updates := map[string]any{
		"open_pr_total":   int(total),
		"open_pr_current": int(current),
		"open_pr_stale":   int(stale),
		"updated_at":      now,
		"last_error":      "",
	}
	for key, value := range extraUpdates {
		updates[key] = value
	}
	return tx.Model(&database.RepoChangeSyncState{}).
		Where("id = ?", state.ID).
		Updates(updates).Error
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
