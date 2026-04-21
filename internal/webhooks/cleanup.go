package webhooks

import (
	"context"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	"gorm.io/gorm"
)

type DeliveryCleanupWorker struct {
	db           *gorm.DB
	retention    time.Duration
	pollInterval time.Duration
	batchSize    int
}

func NewDeliveryCleanupWorker(db *gorm.DB, retention, pollInterval time.Duration, batchSize int) *DeliveryCleanupWorker {
	if pollInterval <= 0 {
		pollInterval = 15 * time.Minute
	}
	if batchSize <= 0 {
		batchSize = 500
	}
	return &DeliveryCleanupWorker{
		db:           db,
		retention:    retention,
		pollInterval: pollInterval,
		batchSize:    batchSize,
	}
}

func (w *DeliveryCleanupWorker) enabled() bool {
	return w != nil && w.db != nil && w.retention > 0 && w.batchSize > 0
}

func (w *DeliveryCleanupWorker) Start(ctx context.Context) error {
	if !w.enabled() {
		return nil
	}

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		if _, err := w.RunOnce(ctx); err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (w *DeliveryCleanupWorker) RunOnce(ctx context.Context) (bool, error) {
	if !w.enabled() {
		return false, nil
	}

	cutoff := time.Now().UTC().Add(-w.retention)
	var ids []uint
	if err := w.db.WithContext(ctx).
		Model(&database.WebhookDelivery{}).
		Where("processed_at IS NOT NULL").
		Where("processed_at < ?", cutoff).
		Order("processed_at ASC").
		Limit(w.batchSize).
		Pluck("id", &ids).Error; err != nil {
		return false, err
	}
	if len(ids) == 0 {
		return false, nil
	}

	result := w.db.WithContext(ctx).
		Where("id IN ?", ids).
		Delete(&database.WebhookDelivery{})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}
