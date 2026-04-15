package githubsync

import (
	"context"
	"errors"
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
	changeBackfillModeOff      = "off"
	changeBackfillModeOpenOnly = "open_only"
)

type RepoBackfillOptions struct {
	MaxPRs     int
	MaxRuntime time.Duration
	LeaseTTL   time.Duration
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
	pull     gh.PullRequestResponse
	snapshot *database.PullRequestChangeSnapshot
}

type repoOpenPullScan struct {
	Result     RepoBackfillResult
	Candidates []backfillCandidate
}

type ChangeSyncWorker struct {
	db                     *gorm.DB
	service                *Service
	pollInterval           time.Duration
	webhookFetchDebounce   time.Duration
	repoMinFetchInterval   time.Duration
	openPRBackfillInterval time.Duration
	leaseTTL               time.Duration
	maxRuntime             time.Duration
	maxPRs                 int
}

func NewChangeSyncWorker(db *gorm.DB, service *Service, pollInterval, webhookFetchDebounce, repoMinFetchInterval, openPRBackfillInterval, leaseTTL, maxRuntime time.Duration, maxPRs int) *ChangeSyncWorker {
	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
	}
	if webhookFetchDebounce <= 0 {
		webhookFetchDebounce = 3 * time.Second
	}
	if repoMinFetchInterval <= 0 {
		repoMinFetchInterval = 15 * time.Second
	}
	if openPRBackfillInterval <= 0 {
		openPRBackfillInterval = time.Minute
	}
	if leaseTTL <= 0 {
		leaseTTL = 5 * time.Minute
	}
	if maxRuntime <= 0 {
		maxRuntime = 30 * time.Second
	}
	if maxPRs <= 0 {
		maxPRs = 10
	}
	return &ChangeSyncWorker{
		db:                     db,
		service:                service,
		pollInterval:           pollInterval,
		webhookFetchDebounce:   webhookFetchDebounce,
		repoMinFetchInterval:   repoMinFetchInterval,
		openPRBackfillInterval: openPRBackfillInterval,
		leaseTTL:               leaseTTL,
		maxRuntime:             maxRuntime,
		maxPRs:                 maxPRs,
	}
}

func (w *ChangeSyncWorker) Start(ctx context.Context) error {
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
	if processed, err := w.processDirtyRepo(ctx); err != nil || processed {
		return processed, err
	}
	return w.processBackfillRepo(ctx)
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

func (s *Service) MarkRepositoryChangeDirty(ctx context.Context, repositoryID uint, seenAt time.Time) error {
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
		status.Dirty = state.Dirty
		status.DirtySince = state.DirtySince
		status.LastWebhookAt = state.LastWebhookAt
		status.LastRequestedFetchAt = state.LastRequestedFetchAt
		status.LastFetchStartedAt = state.LastFetchStartedAt
		status.LastFetchFinishedAt = state.LastFetchFinishedAt
		status.LastSuccessfulFetchAt = state.LastSuccessfulFetchAt
		status.LastBackfillStartedAt = state.LastBackfillStartedAt
		status.LastBackfillFinishedAt = state.LastBackfillFinishedAt
		status.LastOpenPRScanAt = state.LastOpenPRScanAt
		status.BackfillMode = normalizeBackfillMode(state.BackfillMode)
		status.BackfillPriority = state.BackfillPriority
		status.FetchInProgress = state.FetchLeaseUntil != nil && state.FetchLeaseUntil.After(now)
		status.BackfillInProgress = state.BackfillLeaseUntil != nil && state.BackfillLeaseUntil.After(now)
		status.OpenPRTotal = state.OpenPRTotal
		status.OpenPRCurrent = state.OpenPRCurrent
		status.OpenPRStale = state.OpenPRStale
		status.OpenPRMissing = maxInt(0, state.OpenPRTotal-state.OpenPRCurrent-state.OpenPRStale)
		status.OpenPRCursorNumber = state.OpenPRCursorNumber
		status.OpenPRCursorUpdatedAt = state.OpenPRCursorUpdatedAt
		status.LastError = state.LastError
	}
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
		status.BackfillInProgress = state.BackfillLeaseUntil != nil && state.BackfillLeaseUntil.After(now)
		status.RepoDirty = state.Dirty
		status.LastError = state.LastError
	}
	return status, nil
}

func (s *Service) BackfillOpenPullRequests(ctx context.Context, owner, repo string, state database.RepoChangeSyncState, options RepoBackfillOptions) (RepoBackfillResult, error) {
	if s.git == nil {
		return RepoBackfillResult{}, errors.New("git index service is not configured")
	}
	if options.MaxPRs <= 0 {
		options.MaxPRs = 10
	}
	if options.MaxRuntime <= 0 {
		options.MaxRuntime = 30 * time.Second
	}

	repository, err := findRepositoryByName(ctx, s.db, owner, repo)
	if err != nil {
		return RepoBackfillResult{}, err
	}

	scan, err := s.scanOpenPullRequests(ctx, owner, repo, repository.ID, state)
	if err != nil {
		return RepoBackfillResult{}, err
	}
	result := scan.Result
	candidates := scan.Candidates
	deadline := time.Now().UTC().Add(options.MaxRuntime)
	var lastProcessed *backfillCandidate
	for _, candidate := range candidates {
		if result.ProcessedPRs >= options.MaxPRs || time.Now().UTC().After(deadline) {
			break
		}
		if err := s.heartbeatBackfillLease(ctx, state.ID, options.LeaseTTL); err != nil {
			return RepoBackfillResult{}, err
		}
		result.ProcessedPRs++
		if err := s.syncPullRequestChangeOnly(ctx, owner, repo, repository, candidate.pull.Number); err != nil {
			result.FailedPRs++
			continue
		}
		result.IndexedPRs++
		copyCandidate := candidate
		lastProcessed = &copyCandidate
	}

	if lastProcessed != nil {
		idx := findCandidateIndex(candidates, *lastProcessed)
		if idx >= 0 && idx < len(candidates)-1 {
			next := lastProcessed.pull
			result.Completed = false
			result.NextCursorNum = intPtr(next.Number)
			nextTime := next.UpdatedAt.UTC()
			result.NextCursorTime = &nextTime
		} else {
			result.Completed = true
		}
	} else {
		result.Completed = len(candidates) == 0
	}

	postScan, err := s.scanOpenPullRequests(ctx, owner, repo, repository.ID, state)
	if err != nil {
		return RepoBackfillResult{}, err
	}
	result.OpenPRTotal = postScan.Result.OpenPRTotal
	result.OpenPRCurrent = postScan.Result.OpenPRCurrent
	result.OpenPRStale = postScan.Result.OpenPRStale
	result.OpenPRMissing = postScan.Result.OpenPRMissing
	return result, nil
}

func (s *Service) syncPullRequestChangeOnly(ctx context.Context, owner, repo string, canonicalRepo database.Repository, number int) error {
	issue, err := s.github.GetIssue(ctx, owner, repo, number)
	if err != nil {
		return err
	}
	if _, err := s.upsertIssue(ctx, canonicalRepo.ID, issue); err != nil {
		return err
	}
	pull, err := s.github.GetPullRequest(ctx, owner, repo, number)
	if err != nil {
		return err
	}
	if err := s.upsertPullRequest(ctx, canonicalRepo.ID, pull); err != nil {
		return err
	}
	return s.SyncPullRequestIndex(ctx, owner, repo, canonicalRepo.ID, pull)
}

func (w *ChangeSyncWorker) processDirtyRepo(ctx context.Context) (bool, error) {
	now := time.Now().UTC()
	var state database.RepoChangeSyncState
	err := w.db.WithContext(ctx).
		Where("dirty = ? AND (fetch_lease_until IS NULL OR fetch_lease_until <= ?)", true, now).
		Where("dirty_since IS NULL OR dirty_since <= ?", now.Add(-w.webhookFetchDebounce)).
		Where("last_fetch_finished_at IS NULL OR last_fetch_finished_at <= ?", now.Add(-w.repoMinFetchInterval)).
		Order("dirty_since ASC NULLS FIRST, backfill_priority DESC, repository_id ASC").
		First(&state).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}

	leaseUntil := now.Add(w.leaseTTL)
	result := w.db.WithContext(ctx).Model(&database.RepoChangeSyncState{}).
		Where("id = ? AND (fetch_lease_until IS NULL OR fetch_lease_until <= ?)", state.ID, now).
		Updates(map[string]any{
			"fetch_lease_until":     leaseUntil,
			"last_fetch_started_at": now,
			"updated_at":            now,
		})
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected == 0 {
		return false, nil
	}
	state.FetchLeaseUntil = &leaseUntil
	return true, w.runFetchPass(ctx, state)
}

func (w *ChangeSyncWorker) processBackfillRepo(ctx context.Context) (bool, error) {
	now := time.Now().UTC()
	var state database.RepoChangeSyncState
	err := w.db.WithContext(ctx).
		Where("backfill_mode <> ? AND (backfill_lease_until IS NULL OR backfill_lease_until <= ?)", changeBackfillModeOff, now).
		Where("fetch_lease_until IS NULL OR fetch_lease_until <= ?", now).
		Where("last_backfill_finished_at IS NULL OR last_backfill_finished_at <= ?", now.Add(-w.openPRBackfillInterval)).
		Order("backfill_priority DESC, last_backfill_finished_at ASC NULLS FIRST, repository_id ASC").
		First(&state).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}

	leaseUntil := now.Add(w.leaseTTL)
	result := w.db.WithContext(ctx).Model(&database.RepoChangeSyncState{}).
		Where("id = ? AND (backfill_lease_until IS NULL OR backfill_lease_until <= ?)", state.ID, now).
		Updates(map[string]any{
			"backfill_lease_until":     leaseUntil,
			"last_backfill_started_at": now,
			"updated_at":               now,
		})
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected == 0 {
		return false, nil
	}
	state.BackfillLeaseUntil = &leaseUntil
	return true, w.runBackfillPass(ctx, state, false)
}

func (w *ChangeSyncWorker) runFetchPass(ctx context.Context, state database.RepoChangeSyncState) error {
	repository, err := repositoryByID(ctx, w.db, state.RepositoryID)
	if err != nil {
		return w.finishFetchStateWithError(ctx, state, err)
	}
	owner, name, err := splitFullName(repository.FullName)
	if err != nil {
		return w.finishFetchStateWithError(ctx, state, err)
	}

	scan, err := w.service.scanOpenPullRequests(ctx, owner, name, repository.ID, state)
	if err != nil {
		return w.finishFetchStateWithError(ctx, state, err)
	}
	return w.completeFetchPass(ctx, state, scan.Result)
}

func (w *ChangeSyncWorker) runBackfillPass(ctx context.Context, state database.RepoChangeSyncState, _ bool) error {
	repository, err := repositoryByID(ctx, w.db, state.RepositoryID)
	if err != nil {
		return w.finishBackfillStateWithError(ctx, state, err)
	}
	owner, name, err := splitFullName(repository.FullName)
	if err != nil {
		return w.finishBackfillStateWithError(ctx, state, err)
	}

	result, err := w.service.BackfillOpenPullRequests(ctx, owner, name, state, RepoBackfillOptions{
		MaxPRs:     w.maxPRs,
		MaxRuntime: w.maxRuntime,
		LeaseTTL:   w.leaseTTL,
	})
	if err != nil {
		return w.finishBackfillStateWithError(ctx, state, err)
	}

	updates := map[string]any{
		"last_error":                "",
		"open_pr_total":             result.OpenPRTotal,
		"open_pr_current":           result.OpenPRCurrent,
		"open_pr_stale":             result.OpenPRStale,
		"open_pr_cursor_number":     result.NextCursorNum,
		"open_pr_cursor_updated_at": result.NextCursorTime,
		"last_open_pr_scan_at":      time.Now().UTC(),
	}
	return w.completeBackfillPass(ctx, state, updates)
}

func (w *ChangeSyncWorker) completeFetchPass(ctx context.Context, state database.RepoChangeSyncState, result RepoBackfillResult) error {
	now := time.Now().UTC()
	updates := map[string]any{
		"last_error":               "",
		"last_fetch_finished_at":   now,
		"last_successful_fetch_at": now,
		"last_open_pr_scan_at":     now,
		"open_pr_total":            result.OpenPRTotal,
		"open_pr_current":          result.OpenPRCurrent,
		"open_pr_stale":            result.OpenPRStale,
		"fetch_lease_until":        nil,
		"updated_at":               now,
	}
	if err := w.db.WithContext(ctx).Model(&database.RepoChangeSyncState{}).Where("id = ?", state.ID).Updates(updates).Error; err != nil {
		return err
	}

	clearDirty := w.db.WithContext(ctx).Model(&database.RepoChangeSyncState{}).Where("id = ?", state.ID)
	if state.LastRequestedFetchAt != nil {
		clearDirty = clearDirty.Where("(last_requested_fetch_at IS NULL OR last_requested_fetch_at <= ?)", state.LastRequestedFetchAt.UTC())
	}
	if err := clearDirty.Updates(map[string]any{
		"dirty":       false,
		"dirty_since": nil,
		"updated_at":  now,
	}).Error; err != nil {
		return err
	}
	return nil
}

func (w *ChangeSyncWorker) completeBackfillPass(ctx context.Context, state database.RepoChangeSyncState, updates map[string]any) error {
	now := time.Now().UTC()
	updates["backfill_lease_until"] = nil
	updates["last_backfill_finished_at"] = now
	updates["updated_at"] = now
	return w.db.WithContext(ctx).Model(&database.RepoChangeSyncState{}).
		Where("id = ?", state.ID).
		Updates(updates).Error
}

func (w *ChangeSyncWorker) finishFetchStateWithError(ctx context.Context, state database.RepoChangeSyncState, runErr error) error {
	now := time.Now().UTC()
	updates := map[string]any{
		"last_error":             runErr.Error(),
		"last_fetch_finished_at": now,
		"fetch_lease_until":      nil,
		"updated_at":             now,
	}
	if err := w.db.WithContext(ctx).Model(&database.RepoChangeSyncState{}).Where("id = ?", state.ID).Updates(updates).Error; err != nil {
		return err
	}
	return runErr
}

func (w *ChangeSyncWorker) finishBackfillStateWithError(ctx context.Context, state database.RepoChangeSyncState, runErr error) error {
	now := time.Now().UTC()
	updates := map[string]any{
		"last_error":                runErr.Error(),
		"last_backfill_finished_at": now,
		"backfill_lease_until":      nil,
		"updated_at":                now,
	}
	if err := w.db.WithContext(ctx).Model(&database.RepoChangeSyncState{}).Where("id = ?", state.ID).Updates(updates).Error; err != nil {
		return err
	}
	return runErr
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

func (s *Service) scanOpenPullRequests(ctx context.Context, owner, repo string, repositoryID uint, state database.RepoChangeSyncState) (repoOpenPullScan, error) {
	openPulls, err := s.github.ListPullRequests(ctx, owner, repo, "open")
	if err != nil {
		return repoOpenPullScan{}, err
	}
	sort.Slice(openPulls, func(i, j int) bool {
		if openPulls[i].UpdatedAt.Equal(openPulls[j].UpdatedAt) {
			return openPulls[i].Number > openPulls[j].Number
		}
		return openPulls[i].UpdatedAt.After(openPulls[j].UpdatedAt)
	})

	snapshotMap, err := s.pullRequestSnapshotMap(ctx, repositoryID)
	if err != nil {
		return repoOpenPullScan{}, err
	}
	result := RepoBackfillResult{OpenPRTotal: len(openPulls)}
	candidates := make([]backfillCandidate, 0, len(openPulls))
	now := time.Now().UTC()

	for _, pull := range openPulls {
		snapshot := snapshotMap[pull.Number]
		freshness := desiredFreshness(snapshot, pull)
		switch freshness {
		case "current":
			result.OpenPRCurrent++
		case "":
			result.OpenPRMissing++
			candidates = append(candidates, backfillCandidate{pull: pull})
		default:
			result.OpenPRStale++
			candidates = append(candidates, backfillCandidate{pull: pull, snapshot: snapshot})
			if snapshot != nil && snapshot.IndexFreshness != freshness {
				if err := s.db.WithContext(ctx).
					Model(&database.PullRequestChangeSnapshot{}).
					Where("id = ?", snapshot.ID).
					Updates(map[string]any{
						"index_freshness": freshness,
						"updated_at":      now,
					}).Error; err != nil {
					return repoOpenPullScan{}, err
				}
			}
		}
	}

	return repoOpenPullScan{
		Result:     result,
		Candidates: applyCandidateCursor(candidates, state.OpenPRCursorUpdatedAt, state.OpenPRCursorNumber),
	}, nil
}

func normalizeBackfillBaseRef(ref string) string {
	ref = strings.TrimSpace(ref)
	return strings.TrimPrefix(ref, "refs/heads/")
}

func (s *Service) heartbeatBackfillLease(ctx context.Context, stateID uint, leaseTTL time.Duration) error {
	if stateID == 0 || leaseTTL <= 0 {
		return nil
	}
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Model(&database.RepoChangeSyncState{}).
		Where("id = ?", stateID).
		Updates(map[string]any{
			"backfill_lease_until": now.Add(leaseTTL),
			"updated_at":           now,
		}).Error
}

func applyCandidateCursor(candidates []backfillCandidate, cursorTime *time.Time, cursorNumber *int) []backfillCandidate {
	if cursorTime == nil || cursorNumber == nil {
		return candidates
	}
	for i, candidate := range candidates {
		updatedAt := candidate.pull.UpdatedAt.UTC()
		if updatedAt.Before(cursorTime.UTC()) || (updatedAt.Equal(cursorTime.UTC()) && candidate.pull.Number < *cursorNumber) {
			return candidates[i:]
		}
	}
	return nil
}

func findCandidateIndex(candidates []backfillCandidate, target backfillCandidate) int {
	for i, candidate := range candidates {
		if candidate.pull.Number == target.pull.Number && candidate.pull.UpdatedAt.Equal(target.pull.UpdatedAt) {
			return i
		}
	}
	return -1
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
