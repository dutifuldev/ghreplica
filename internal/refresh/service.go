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
	jobType := strings.TrimSpace(request.JobType)
	if jobType == "" {
		jobType = JobTypeBootstrapRepository
	}

	now := time.Now().UTC()
	tracked, err := ResolveTrackedRepository(ctx, s.db, nil, request.FullName)
	if err != nil {
		return err
	}

	repository, err := resolveRepositoryForRefresh(ctx, s.db, tracked, request.FullName)
	if err != nil {
		return err
	}

	query := s.db.WithContext(ctx).
		Where("job_type = ? AND ((status = ?) OR (status = ? AND (lease_expires_at IS NULL OR lease_expires_at > ?)))",
			jobType,
			"pending",
			"processing",
			now,
		)
	if repository != nil {
		query = query.Where("repository_id = ?", repository.ID)
	} else if tracked != nil && tracked.RepositoryID != nil {
		query = query.Where("repository_id = ?", *tracked.RepositoryID)
	} else {
		query = query.Where("full_name = ?", request.FullName)
	}

	var existing database.RepositoryRefreshJob
	err = query.Order("id ASC").First(&existing).Error
	if err == nil {
		return nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

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

	return s.db.WithContext(ctx).Create(&job).Error
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

	err = w.bootstrapper.BootstrapRepository(ctx, job.Owner, job.Name)
	if err != nil {
		return true, w.markFailed(ctx, job.ID, err)
	}

	return true, w.markSucceeded(ctx, job)
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
	if err := db.WithContext(ctx).Create(&tracked).Error; err != nil {
		return database.TrackedRepository{}, err
	}

	var stored database.TrackedRepository
	err = db.WithContext(ctx).First(&stored, tracked.ID).Error
	return stored, err
}

func ResolveTrackedRepository(ctx context.Context, db *gorm.DB, repositoryID *uint, fullName string) (*database.TrackedRepository, error) {
	var (
		byRepository *database.TrackedRepository
		byFullName   *database.TrackedRepository
	)

	if repositoryID != nil {
		var tracked database.TrackedRepository
		err := db.WithContext(ctx).Where("repository_id = ?", *repositoryID).Order("id ASC").First(&tracked).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if err == nil {
			byRepository = &tracked
		}
	}

	fullName = strings.TrimSpace(fullName)
	if fullName != "" {
		var tracked database.TrackedRepository
		err := db.WithContext(ctx).Where("full_name = ?", fullName).First(&tracked).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if err == nil {
			byFullName = &tracked
		}
	}

	switch {
	case byRepository == nil && byFullName == nil:
		return nil, nil
	case byRepository != nil && (byFullName == nil || byRepository.ID == byFullName.ID):
		return byRepository, nil
	case byRepository == nil:
		return byFullName, nil
	default:
		if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&database.RepositoryRefreshJob{}).
				Where("tracked_repository_id = ?", byFullName.ID).
				Update("tracked_repository_id", byRepository.ID).Error; err != nil {
				return err
			}
			return tx.Delete(&database.TrackedRepository{}, byFullName.ID).Error
		}); err != nil {
			return nil, err
		}

		var stored database.TrackedRepository
		if err := db.WithContext(ctx).First(&stored, byRepository.ID).Error; err != nil {
			return nil, err
		}
		return &stored, nil
	}
}

func resolveRepositoryForRefresh(ctx context.Context, db *gorm.DB, tracked *database.TrackedRepository, fullName string) (*database.Repository, error) {
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

	if strings.TrimSpace(fullName) == "" {
		return nil, nil
	}

	var repository database.Repository
	err := db.WithContext(ctx).Preload("Owner").Where("full_name = ?", fullName).First(&repository).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &repository, nil
}

func resolveRepositoryForJob(ctx context.Context, db *gorm.DB, job database.RepositoryRefreshJob) (*database.Repository, error) {
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

	if strings.TrimSpace(job.FullName) == "" {
		return nil, nil
	}

	var repository database.Repository
	err := db.WithContext(ctx).Preload("Owner").Where("full_name = ?", job.FullName).First(&repository).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &repository, nil
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
