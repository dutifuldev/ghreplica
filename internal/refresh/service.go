package refresh

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/dutifuldev/ghreplica/internal/github"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Request struct {
	Owner      string
	Name       string
	FullName   string
	Source     string
	DeliveryID string
}

type Scheduler struct {
	db *gorm.DB
}

func NewScheduler(db *gorm.DB) *Scheduler {
	return &Scheduler{db: db}
}

func (s *Scheduler) EnqueueRepositoryRefresh(ctx context.Context, request Request) error {
	var existing database.RepositoryRefreshJob
	err := s.db.WithContext(ctx).
		Where("full_name = ? AND status IN ?", request.FullName, []string{"pending", "processing"}).
		Order("id ASC").
		First(&existing).Error
	if err == nil {
		return nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	var tracked database.TrackedRepository
	err = s.db.WithContext(ctx).Where("full_name = ?", request.FullName).First(&tracked).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	var repository database.Repository
	err = s.db.WithContext(ctx).Where("full_name = ?", request.FullName).First(&repository).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	now := time.Now().UTC()
	job := database.RepositoryRefreshJob{
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
	if err == nil {
		job.RepositoryID = &repository.ID
	}
	if tracked.ID != 0 {
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
}

func NewWorker(db *gorm.DB, bootstrapper Bootstrapper, pollInterval time.Duration) *Worker {
	if pollInterval <= 0 {
		pollInterval = 2 * time.Second
	}
	return &Worker{db: db, bootstrapper: bootstrapper, pollInterval: pollInterval}
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

	return true, w.markSucceeded(ctx, job.ID, job.FullName)
}

func (w *Worker) claimNextJob(ctx context.Context) (database.RepositoryRefreshJob, bool, error) {
	var job database.RepositoryRefreshJob
	now := time.Now().UTC()
	err := w.db.WithContext(ctx).
		Where("status = ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?)", "pending", now).
		Order("requested_at ASC, id ASC").
		First(&job).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return database.RepositoryRefreshJob{}, false, nil
		}
		return database.RepositoryRefreshJob{}, false, err
	}

	result := w.db.WithContext(ctx).Model(&database.RepositoryRefreshJob{}).
		Where("id = ? AND status = ?", job.ID, "pending").
		Updates(map[string]any{
			"status":     "processing",
			"attempts":   gorm.Expr("attempts + 1"),
			"started_at": now,
			"updated_at": now,
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
	return job, true, nil
}

func (w *Worker) markSucceeded(ctx context.Context, jobID uint, fullName string) error {
	now := time.Now().UTC()

	var repository database.Repository
	err := w.db.WithContext(ctx).Where("full_name = ?", fullName).First(&repository).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	updates := map[string]any{
		"status":          "succeeded",
		"last_error":      "",
		"finished_at":     now,
		"next_attempt_at": nil,
		"updated_at":      now,
	}
	if err == nil {
		updates["repository_id"] = repository.ID
	}

	if err := w.db.WithContext(ctx).Model(&database.RepositoryRefreshJob{}).
		Where("id = ?", jobID).
		Updates(updates).Error; err != nil {
		return err
	}

	if err == nil {
		return w.db.WithContext(ctx).Model(&database.TrackedRepository{}).
			Where("full_name = ?", fullName).
			Updates(map[string]any{
				"repository_id": repository.ID,
				"last_crawl_at": now,
				"updated_at":    now,
			}).Error
	}

	return nil
}

func (w *Worker) markFailed(ctx context.Context, jobID uint, reason error) error {
	now := time.Now().UTC()
	updates := map[string]any{
		"last_error":  reason.Error(),
		"finished_at": now,
		"updated_at":  now,
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

func UpsertTrackedRepositoryForWebhook(ctx context.Context, db *gorm.DB, owner, name, fullName string, seenAt time.Time) (database.TrackedRepository, error) {
	tracked := database.TrackedRepository{
		Owner:         owner,
		Name:          name,
		FullName:      fullName,
		SyncMode:      "webhook",
		Enabled:       true,
		LastWebhookAt: &seenAt,
	}
	if err := db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "full_name"}},
		DoUpdates: clause.Assignments(map[string]any{
			"owner":           owner,
			"name":            name,
			"sync_mode":       "webhook",
			"enabled":         true,
			"last_webhook_at": seenAt,
			"updated_at":      seenAt,
		}),
	}).Create(&tracked).Error; err != nil {
		return database.TrackedRepository{}, err
	}

	var stored database.TrackedRepository
	err := db.WithContext(ctx).Where("full_name = ?", fullName).First(&stored).Error
	return stored, err
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
