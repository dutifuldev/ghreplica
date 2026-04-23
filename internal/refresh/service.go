package refresh

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/dutifuldev/ghreplica/internal/github"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Request struct {
	JobType    string
	Owner      string
	Name       string
	FullName   string
	Source     string
	DeliveryID string
}

const (
	JobTypeBootstrapRepository = "bootstrap_repository"
	syncModeWebhookOnly        = "webhook_only"
	syncModeManualBackfill     = "manual_backfill"
	completenessEmpty          = "empty"
	completenessSparse         = "sparse"
	completenessBackfilled     = "backfilled"
)

type Scheduler struct {
	db *gorm.DB
}

func NewScheduler(db *gorm.DB) *Scheduler {
	return &Scheduler{db: db}
}

func (s *Scheduler) EnqueueRepositoryRefresh(ctx context.Context, request Request) error {
	jobType := normalizeRefreshJobType(request.JobType)
	now := time.Now().UTC()
	tracked, err := ResolveTrackedRepository(ctx, s.db, nil, request.FullName)
	if err != nil {
		return err
	}

	repository, err := resolveRepositoryForRefresh(ctx, s.db, tracked, request.FullName)
	if err != nil {
		return err
	}

	exists, err := refreshJobExists(ctx, s.db, tracked, repository, request.FullName, jobType, now)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	job := buildRepositoryRefreshJob(request, jobType, tracked, repository, now)
	return s.db.WithContext(ctx).Create(&job).Error
}

func normalizeRefreshJobType(jobType string) string {
	jobType = strings.TrimSpace(jobType)
	if jobType == "" {
		return JobTypeBootstrapRepository
	}
	return jobType
}

func refreshJobExists(ctx context.Context, db *gorm.DB, tracked *database.TrackedRepository, repository *database.Repository, fullName, jobType string, now time.Time) (bool, error) {
	query := db.WithContext(ctx).
		Where("job_type = ? AND ((status = ?) OR (status = ? AND (lease_expires_at IS NULL OR lease_expires_at > ?)))",
			jobType,
			"pending",
			"processing",
			now,
		).
		Where(refreshJobIdentityCondition(db.WithContext(ctx), tracked, repository, fullName))

	var existing database.RepositoryRefreshJob
	err := query.Order("id ASC").First(&existing).Error
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		return false, nil
	default:
		return false, err
	}
}

func buildRepositoryRefreshJob(request Request, jobType string, tracked *database.TrackedRepository, repository *database.Repository, now time.Time) database.RepositoryRefreshJob {
	job := database.RepositoryRefreshJob{
		JobType:       jobType,
		Owner:         request.Owner,
		Name:          request.Name,
		FullName:      request.FullName,
		Source:        request.Source,
		DeliveryID:    request.DeliveryID,
		Status:        "pending",
		MaxAttempts:   3,
		RequestedAt:   now,
		NextAttemptAt: &now,
	}
	if repository != nil {
		job.Owner = repository.OwnerLogin
		job.Name = repository.Name
		job.FullName = repository.FullName
		job.RepositoryID = &repository.ID
	}
	if tracked != nil {
		job.TrackedRepositoryID = &tracked.ID
		if tracked.RepositoryID != nil && job.RepositoryID == nil {
			job.RepositoryID = tracked.RepositoryID
		}
	}
	return job
}

type Bootstrapper interface {
	BootstrapRepository(ctx context.Context, owner, repo string) error
}

type Worker struct {
	db           *gorm.DB
	bootstrapper Bootstrapper
	pollInterval time.Duration
	leaseTTL     time.Duration
}

func NewWorker(db *gorm.DB, bootstrapper Bootstrapper, pollInterval time.Duration) *Worker {
	if pollInterval <= 0 {
		pollInterval = 2 * time.Second
	}
	return &Worker{
		db:           db,
		bootstrapper: bootstrapper,
		pollInterval: pollInterval,
		leaseTTL:     5 * time.Minute,
	}
}

func (w *Worker) Start(ctx context.Context) error {
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		if _, err := w.RunOnce(ctx); err != nil {
			slog.Error("refresh worker iteration failed", "error", err)
			return err
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	if err := w.supersedeWebhookRefreshJobs(ctx); err != nil {
		return false, err
	}
	if err := w.recoverExpiredLeases(ctx); err != nil {
		return false, err
	}

	job, claimed, err := w.claimNextJob(ctx)
	if err != nil {
		return false, err
	}
	if !claimed {
		return false, nil
	}

	owner, name, err := w.resolveJobLocator(ctx, job)
	if err != nil {
		return true, w.markFailed(ctx, job.ID, err)
	}

	err = w.bootstrapper.BootstrapRepository(ctx, owner, name)
	if err != nil {
		return true, w.markFailed(ctx, job.ID, err)
	}

	return true, w.markSucceeded(ctx, job)
}

func (w *Worker) resolveJobLocator(ctx context.Context, job database.RepositoryRefreshJob) (string, string, error) {
	owner := strings.TrimSpace(job.Owner)
	name := strings.TrimSpace(job.Name)

	tracked, err := w.lookupTrackedRepositoryLocator(ctx, job.TrackedRepositoryID)
	if err != nil {
		return "", "", err
	}
	owner, name = coalesceJobLocator(owner, name, tracked.Owner, tracked.Name)

	repo, err := resolveRepositoryForJob(ctx, w.db, job)
	if err != nil {
		return "", "", err
	}
	if repo != nil {
		owner, name = coalesceJobLocator(owner, name, repo.OwnerLogin, repo.Name)
	}

	if owner == "" || name == "" {
		return "", "", errors.New("refresh job is missing a repository locator")
	}

	return owner, name, nil
}

func (w *Worker) lookupTrackedRepositoryLocator(ctx context.Context, trackedRepositoryID *uint) (database.TrackedRepository, error) {
	if trackedRepositoryID == nil {
		return database.TrackedRepository{}, nil
	}
	var tracked database.TrackedRepository
	err := w.db.WithContext(ctx).First(&tracked, *trackedRepositoryID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return database.TrackedRepository{}, nil
	}
	return tracked, err
}

func coalesceJobLocator(owner, name string, candidates ...string) (string, string) {
	trimmed := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		trimmed = append(trimmed, strings.TrimSpace(candidate))
	}
	if len(trimmed) > 0 && trimmed[0] != "" {
		owner = trimmed[0]
	}
	if len(trimmed) > 1 && trimmed[1] != "" {
		name = trimmed[1]
	}
	return owner, name
}

func (w *Worker) claimNextJob(ctx context.Context) (database.RepositoryRefreshJob, bool, error) {
	var job database.RepositoryRefreshJob
	now := time.Now().UTC()
	err := w.db.WithContext(ctx).
		Where("job_type = ? AND status = ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?)", JobTypeBootstrapRepository, "pending", now).
		Order("requested_at ASC, id ASC").
		First(&job).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return database.RepositoryRefreshJob{}, false, nil
		}
		return database.RepositoryRefreshJob{}, false, err
	}

	leaseExpiresAt := now.Add(w.leaseTTL)
	result := w.db.WithContext(ctx).Model(&database.RepositoryRefreshJob{}).
		Where("id = ? AND status = ?", job.ID, "pending").
		Updates(map[string]any{
			"status":           "processing",
			"attempts":         gorm.Expr("attempts + 1"),
			"started_at":       now,
			"lease_expires_at": leaseExpiresAt,
			"updated_at":       now,
		})
	if result.Error != nil {
		return database.RepositoryRefreshJob{}, false, result.Error
	}
	if result.RowsAffected == 0 {
		return database.RepositoryRefreshJob{}, false, nil
	}

	job.Status = "processing"
	job.Attempts++
	job.StartedAt = &now
	job.LeaseExpiresAt = &leaseExpiresAt
	return job, true, nil
}

func (w *Worker) markSucceeded(ctx context.Context, job database.RepositoryRefreshJob) error {
	now := time.Now().UTC()

	repository, err := resolveRepositoryForJob(ctx, w.db, job)
	if err != nil {
		return err
	}

	updates := map[string]any{
		"status":           "succeeded",
		"last_error":       "",
		"finished_at":      now,
		"next_attempt_at":  nil,
		"lease_expires_at": nil,
		"updated_at":       now,
	}
	if repository != nil {
		updates["repository_id"] = repository.ID
		updates["owner"] = repository.OwnerLogin
		updates["name"] = repository.Name
		updates["full_name"] = repository.FullName
	}

	if err := w.db.WithContext(ctx).Model(&database.RepositoryRefreshJob{}).
		Where("id = ?", job.ID).
		Updates(updates).Error; err != nil {
		return err
	}

	if repository != nil {
		tracked, err := ResolveTrackedRepository(ctx, w.db, &repository.ID, repository.FullName)
		if err != nil {
			return err
		}
		if tracked == nil {
			return nil
		}

		return w.db.WithContext(ctx).Model(&database.TrackedRepository{}).
			Where("id = ?", tracked.ID).
			Updates(map[string]any{
				"owner":                      repository.OwnerLogin,
				"name":                       repository.Name,
				"full_name":                  repository.FullName,
				"repository_id":              repository.ID,
				"sync_mode":                  syncModeManualBackfill,
				"allow_manual_backfill":      true,
				"issues_completeness":        completenessBackfilled,
				"pulls_completeness":         completenessBackfilled,
				"comments_completeness":      completenessBackfilled,
				"reviews_completeness":       completenessBackfilled,
				"last_bootstrap_at":          now,
				"last_crawl_at":              now,
				"webhook_projection_enabled": true,
				"updated_at":                 now,
			}).Error
	}

	return nil
}

func (w *Worker) markFailed(ctx context.Context, jobID uint, reason error) error {
	now := time.Now().UTC()
	updates := map[string]any{
		"last_error":       reason.Error(),
		"finished_at":      now,
		"lease_expires_at": nil,
		"updated_at":       now,
	}

	var httpErr *github.HTTPError
	if errors.As(reason, &httpErr) && httpErr.Temporary() {
		var job database.RepositoryRefreshJob
		if err := w.db.WithContext(ctx).First(&job, jobID).Error; err != nil {
			return err
		}
		if job.Attempts < job.MaxAttempts {
			retryAt := now.Add(backoffForAttempt(job.Attempts))
			updates["status"] = "pending"
			updates["next_attempt_at"] = retryAt
			updates["started_at"] = nil
			updates["lease_expires_at"] = nil
			return w.db.WithContext(ctx).Model(&database.RepositoryRefreshJob{}).
				Where("id = ?", jobID).
				Updates(updates).Error
		}
	}

	updates["status"] = "failed"
	updates["next_attempt_at"] = nil
	return w.db.WithContext(ctx).Model(&database.RepositoryRefreshJob{}).
		Where("id = ?", jobID).
		Updates(updates).Error
}

func UpsertTrackedRepositoryForWebhook(ctx context.Context, db *gorm.DB, owner, name, fullName string, repositoryID *uint, seenAt time.Time) (database.TrackedRepository, error) {
	existing, err := ResolveTrackedRepository(ctx, db, repositoryID, fullName)
	if err != nil {
		return database.TrackedRepository{}, err
	}

	if existing != nil {
		updates := map[string]any{
			"owner":           owner,
			"name":            name,
			"full_name":       fullName,
			"last_webhook_at": seenAt,
			"updated_at":      seenAt,
		}
		if repositoryID != nil {
			updates["repository_id"] = *repositoryID
		}
		if err := db.WithContext(ctx).Model(&database.TrackedRepository{}).
			Where("id = ?", existing.ID).
			Updates(updates).Error; err != nil {
			return database.TrackedRepository{}, err
		}

		var stored database.TrackedRepository
		if err := db.WithContext(ctx).First(&stored, existing.ID).Error; err != nil {
			return database.TrackedRepository{}, err
		}
		return stored, nil
	}

	tracked := database.TrackedRepository{
		Owner:                    owner,
		Name:                     name,
		FullName:                 fullName,
		SyncMode:                 syncModeWebhookOnly,
		WebhookProjectionEnabled: true,
		AllowManualBackfill:      false,
		IssuesCompleteness:       completenessEmpty,
		PullsCompleteness:        completenessEmpty,
		CommentsCompleteness:     completenessEmpty,
		ReviewsCompleteness:      completenessEmpty,
		Enabled:                  true,
		LastWebhookAt:            &seenAt,
	}
	if repositoryID != nil {
		tracked.RepositoryID = repositoryID
	}

	assignments := map[string]any{
		"owner":           owner,
		"name":            name,
		"last_webhook_at": seenAt,
		"updated_at":      seenAt,
	}
	if repositoryID != nil {
		assignments["repository_id"] = *repositoryID
	}

	if err := db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "full_name"}},
		DoUpdates: clause.Assignments(assignments),
	}).Create(&tracked).Error; err != nil {
		return database.TrackedRepository{}, err
	}

	var stored database.TrackedRepository
	err = db.WithContext(ctx).First(&stored, tracked.ID).Error
	return stored, err
}

func ResolveTrackedRepository(ctx context.Context, db *gorm.DB, repositoryID *uint, fullName string) (*database.TrackedRepository, error) {
	byRepository, err := trackedRepositoryByRepositoryID(ctx, db, repositoryID)
	if err != nil {
		return nil, err
	}
	byFullName, err := trackedRepositoryByFullName(ctx, db, fullName)
	if err != nil {
		return nil, err
	}
	switch resolveTrackedRepositoryMode(byRepository, byFullName) {
	case trackedRepositoryResolutionNone:
		return nil, nil
	case trackedRepositoryResolutionRepository:
		return byRepository, nil
	case trackedRepositoryResolutionFullName:
		return byFullName, nil
	default:
		if err := mergeTrackedRepositoryMatches(ctx, db, *byRepository, *byFullName); err != nil {
			return nil, err
		}
		return reloadTrackedRepository(ctx, db, byRepository.ID)
	}
}

type trackedRepositoryResolution string

const (
	trackedRepositoryResolutionNone       trackedRepositoryResolution = "none"
	trackedRepositoryResolutionRepository trackedRepositoryResolution = "repository"
	trackedRepositoryResolutionFullName   trackedRepositoryResolution = "full_name"
	trackedRepositoryResolutionMerge      trackedRepositoryResolution = "merge"
)

func trackedRepositoryByRepositoryID(ctx context.Context, db *gorm.DB, repositoryID *uint) (*database.TrackedRepository, error) {
	if repositoryID == nil {
		return nil, nil
	}
	return firstTrackedRepositoryMatch(ctx, db, "repository_id = ?", *repositoryID)
}

func trackedRepositoryByFullName(ctx context.Context, db *gorm.DB, fullName string) (*database.TrackedRepository, error) {
	fullName = strings.TrimSpace(fullName)
	if fullName == "" {
		return nil, nil
	}
	return firstTrackedRepositoryMatch(ctx, db, "full_name = ?", fullName)
}

func firstTrackedRepositoryMatch(ctx context.Context, db *gorm.DB, query string, args ...any) (*database.TrackedRepository, error) {
	var tracked database.TrackedRepository
	err := db.WithContext(ctx).Where(query, args...).Order("id ASC").First(&tracked).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &tracked, nil
}

func resolveTrackedRepositoryMode(byRepository, byFullName *database.TrackedRepository) trackedRepositoryResolution {
	switch {
	case byRepository == nil && byFullName == nil:
		return trackedRepositoryResolutionNone
	case byRepository != nil && (byFullName == nil || byRepository.ID == byFullName.ID):
		return trackedRepositoryResolutionRepository
	case byRepository == nil:
		return trackedRepositoryResolutionFullName
	default:
		return trackedRepositoryResolutionMerge
	}
}

func mergeTrackedRepositoryMatches(ctx context.Context, db *gorm.DB, byRepository, byFullName database.TrackedRepository) error {
	mergedUpdates := mergeTrackedRepositoryRows(byRepository, byFullName)
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := updateRepositoryRefreshJobs(tx, byFullName.ID, byRepository.ID, mergedUpdates); err != nil {
			return err
		}
		if err := tx.Delete(&database.TrackedRepository{}, byFullName.ID).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.TrackedRepository{}).
			Where("id = ?", byRepository.ID).
			Updates(mergedUpdates).Error; err != nil {
			return err
		}
		return updateRepositoryRefreshJobs(tx, byRepository.ID, byRepository.ID, mergedUpdates)
	})
}

func updateRepositoryRefreshJobs(tx *gorm.DB, fromTrackedRepositoryID, toTrackedRepositoryID uint, mergedUpdates map[string]any) error {
	return tx.Model(&database.RepositoryRefreshJob{}).
		Where("tracked_repository_id = ?", fromTrackedRepositoryID).
		Updates(map[string]any{
			"tracked_repository_id": toTrackedRepositoryID,
			"repository_id":         mergedUpdates["repository_id"],
			"owner":                 mergedUpdates["owner"],
			"name":                  mergedUpdates["name"],
			"full_name":             mergedUpdates["full_name"],
		}).Error
}

func reloadTrackedRepository(ctx context.Context, db *gorm.DB, trackedRepositoryID uint) (*database.TrackedRepository, error) {
	var stored database.TrackedRepository
	if err := db.WithContext(ctx).First(&stored, trackedRepositoryID).Error; err != nil {
		return nil, err
	}
	return &stored, nil
}

func mergeTrackedRepositoryRows(stable, current database.TrackedRepository) map[string]any {
	repositoryID := stable.RepositoryID
	if repositoryID == nil {
		repositoryID = current.RepositoryID
	}

	return map[string]any{
		"owner":                      firstNonEmpty(current.Owner, stable.Owner),
		"name":                       firstNonEmpty(current.Name, stable.Name),
		"full_name":                  firstNonEmpty(current.FullName, stable.FullName),
		"repository_id":              repositoryID,
		"sync_mode":                  firstNonEmpty(stable.SyncMode, current.SyncMode),
		"webhook_projection_enabled": stable.WebhookProjectionEnabled,
		"allow_manual_backfill":      stable.AllowManualBackfill,
		"enabled":                    stable.Enabled,
		"issues_completeness":        mergeCompleteness(stable.IssuesCompleteness, current.IssuesCompleteness),
		"pulls_completeness":         mergeCompleteness(stable.PullsCompleteness, current.PullsCompleteness),
		"comments_completeness":      mergeCompleteness(stable.CommentsCompleteness, current.CommentsCompleteness),
		"reviews_completeness":       mergeCompleteness(stable.ReviewsCompleteness, current.ReviewsCompleteness),
		"last_bootstrap_at":          laterTime(stable.LastBootstrapAt, current.LastBootstrapAt),
		"last_crawl_at":              laterTime(stable.LastCrawlAt, current.LastCrawlAt),
		"last_webhook_at":            laterTime(stable.LastWebhookAt, current.LastWebhookAt),
	}
}

func mergeCompleteness(stable, current string) string {
	if completenessRank(current) > completenessRank(stable) {
		return current
	}
	return stable
}

func completenessRank(value string) int {
	switch strings.TrimSpace(value) {
	case completenessBackfilled:
		return 3
	case completenessSparse:
		return 2
	case completenessEmpty:
		return 1
	default:
		return 0
	}
}

func laterTime(stable, current *time.Time) *time.Time {
	switch {
	case stable == nil:
		return current
	case current == nil:
		return stable
	case current.After(*stable):
		return current
	default:
		return stable
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func refreshJobIdentityCondition(db *gorm.DB, tracked *database.TrackedRepository, repository *database.Repository, fullName string) *gorm.DB {
	condition := db.Where("1 = 0")
	if repository != nil {
		condition = condition.Or("repository_id = ?", repository.ID)
	}
	if tracked != nil {
		condition = condition.Or("tracked_repository_id = ?", tracked.ID)
		if tracked.RepositoryID != nil {
			condition = condition.Or("repository_id = ?", *tracked.RepositoryID)
		}
	}
	fullName = strings.TrimSpace(fullName)
	if fullName != "" {
		// Fallback for older jobs that predate stable helper IDs.
		condition = condition.Or("(repository_id IS NULL AND tracked_repository_id IS NULL AND full_name = ?)", fullName)
	}
	return condition
}

func resolveRepositoryForRefresh(ctx context.Context, db *gorm.DB, tracked *database.TrackedRepository, fullName string) (*database.Repository, error) {
	if strings.TrimSpace(fullName) != "" {
		var repository database.Repository
		err := db.WithContext(ctx).Preload("Owner").Where("full_name = ?", fullName).First(&repository).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if err == nil {
			return &repository, nil
		}
	}

	if tracked != nil && tracked.RepositoryID != nil {
		var repository database.Repository
		err := db.WithContext(ctx).Preload("Owner").First(&repository, *tracked.RepositoryID).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if err == nil {
			return &repository, nil
		}
	}

	return nil, nil
}

func resolveRepositoryForJob(ctx context.Context, db *gorm.DB, job database.RepositoryRefreshJob) (*database.Repository, error) {
	if strings.TrimSpace(job.FullName) != "" {
		var repository database.Repository
		err := db.WithContext(ctx).Preload("Owner").Where("full_name = ?", job.FullName).First(&repository).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if err == nil {
			return &repository, nil
		}
	}

	if job.RepositoryID != nil {
		var repository database.Repository
		err := db.WithContext(ctx).Preload("Owner").First(&repository, *job.RepositoryID).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if err == nil {
			return &repository, nil
		}
	}

	return nil, nil
}

func CompletenessUpdatesForEvent(event string) map[string]any {
	switch event {
	case "issues":
		return map[string]any{"issues_completeness": completenessSparse}
	case "issue_comment":
		return map[string]any{
			"issues_completeness":   completenessSparse,
			"comments_completeness": completenessSparse,
		}
	case "pull_request":
		return map[string]any{
			"issues_completeness": completenessSparse,
			"pulls_completeness":  completenessSparse,
		}
	case "pull_request_review":
		return map[string]any{
			"issues_completeness":  completenessSparse,
			"pulls_completeness":   completenessSparse,
			"reviews_completeness": completenessSparse,
		}
	case "pull_request_review_comment":
		return map[string]any{
			"issues_completeness":   completenessSparse,
			"pulls_completeness":    completenessSparse,
			"comments_completeness": completenessSparse,
			"reviews_completeness":  completenessSparse,
		}
	case "repository", "ping", "push":
		return map[string]any{}
	default:
		return map[string]any{}
	}
}

func (w *Worker) supersedeWebhookRefreshJobs(ctx context.Context) error {
	now := time.Now().UTC()
	return w.db.WithContext(ctx).Model(&database.RepositoryRefreshJob{}).
		Where("source = ? AND status IN ?", "webhook", []string{"pending", "processing", "failed"}).
		Updates(map[string]any{
			"status":           "superseded",
			"last_error":       "superseded by direct webhook projection",
			"finished_at":      now,
			"next_attempt_at":  nil,
			"lease_expires_at": nil,
			"updated_at":       now,
		}).Error
}

func (w *Worker) recoverExpiredLeases(ctx context.Context) error {
	now := time.Now().UTC()
	var jobs []database.RepositoryRefreshJob
	if err := w.db.WithContext(ctx).
		Where("status = ? AND ((lease_expires_at IS NOT NULL AND lease_expires_at <= ?) OR (lease_expires_at IS NULL AND started_at IS NOT NULL AND started_at <= ?))",
			"processing",
			now,
			now.Add(-w.leaseTTL),
		).
		Find(&jobs).Error; err != nil {
		return err
	}

	for _, job := range jobs {
		updates := map[string]any{
			"lease_expires_at": nil,
			"started_at":       nil,
			"finished_at":      now,
			"updated_at":       now,
		}
		if job.Attempts < job.MaxAttempts {
			updates["status"] = "pending"
			updates["next_attempt_at"] = now
		} else {
			updates["status"] = "failed"
			updates["next_attempt_at"] = nil
			updates["last_error"] = "job lease expired"
		}
		if err := w.db.WithContext(ctx).Model(&database.RepositoryRefreshJob{}).
			Where("id = ? AND status = ?", job.ID, "processing").
			Updates(updates).Error; err != nil {
			return err
		}
	}

	return nil
}

func backoffForAttempt(attempt int) time.Duration {
	switch attempt {
	case 0, 1:
		return 15 * time.Second
	case 2:
		return time.Minute
	default:
		return 5 * time.Minute
	}
}
