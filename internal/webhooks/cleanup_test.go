package webhooks_test

import (
	"context"
	"testing"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/dutifuldev/ghreplica/internal/webhooks"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDeliveryCleanupWorkerCompactsOnlyOldProcessedRows(t *testing.T) {
	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))
	ctx := context.Background()
	now := time.Now().UTC()
	oldProcessedAt := now.Add(-48 * time.Hour)
	recentProcessedAt := now.Add(-2 * time.Hour)

	deliveries := []database.WebhookDelivery{
		{DeliveryID: "old-processed", Event: "ping", HeadersJSON: []byte(`{"header":"old"}`), PayloadJSON: []byte(`{"payload":"old"}`), ReceivedAt: oldProcessedAt, ProcessedAt: &oldProcessedAt},
		{DeliveryID: "recent-processed", Event: "ping", HeadersJSON: []byte(`{"header":"recent"}`), PayloadJSON: []byte(`{"payload":"recent"}`), ReceivedAt: recentProcessedAt, ProcessedAt: &recentProcessedAt},
		{DeliveryID: "old-unprocessed", Event: "ping", HeadersJSON: []byte(`{"header":"pending"}`), PayloadJSON: []byte(`{"payload":"pending"}`), ReceivedAt: oldProcessedAt},
	}
	for _, delivery := range deliveries {
		require.NoError(t, db.WithContext(ctx).Create(&delivery).Error)
	}

	worker := webhooks.NewDeliveryCleanupWorker(db, 24*time.Hour, time.Minute, 100)
	processed, err := worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)

	requireWebhookDeliveryDeleted(t, db, "old-processed")
	requireWebhookDeliveryPresent(t, db, "recent-processed")
	requireWebhookDeliveryPresent(t, db, "old-unprocessed")
}

func TestDeliveryCleanupWorkerDeletesPreviouslyCompactedRowsToo(t *testing.T) {
	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))
	ctx := context.Background()
	now := time.Now().UTC()
	processedAt := now.Add(-72 * time.Hour)
	compactedAt := now.Add(-48 * time.Hour)

	delivery := database.WebhookDelivery{
		DeliveryID:  "old-compacted",
		Event:       "ping",
		HeadersJSON: []byte(`{}`),
		PayloadJSON: []byte(`{}`),
		ReceivedAt:  processedAt,
		ProcessedAt: &processedAt,
		CompactedAt: &compactedAt,
	}
	require.NoError(t, db.WithContext(ctx).Create(&delivery).Error)

	worker := webhooks.NewDeliveryCleanupWorker(db, 24*time.Hour, time.Minute, 100)
	processed, err := worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)

	requireWebhookDeliveryDeleted(t, db, "old-compacted")
}

func TestDeliveryCleanupWorkerRespectsBatchSizeWhenDeleting(t *testing.T) {
	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))
	ctx := context.Background()
	now := time.Now().UTC()
	processedAt := now.Add(-72 * time.Hour)

	for _, deliveryID := range []string{"old-1", "old-2", "old-3"} {
		delivery := database.WebhookDelivery{
			DeliveryID:  deliveryID,
			Event:       "ping",
			HeadersJSON: []byte(`{"header":"old"}`),
			PayloadJSON: []byte(`{"payload":"old"}`),
			ReceivedAt:  processedAt,
			ProcessedAt: &processedAt,
		}
		require.NoError(t, db.WithContext(ctx).Create(&delivery).Error)
	}

	worker := webhooks.NewDeliveryCleanupWorker(db, 24*time.Hour, time.Minute, 2)
	processed, err := worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)

	var remaining int64
	require.NoError(t, db.WithContext(ctx).
		Model(&database.WebhookDelivery{}).
		Where("processed_at IS NOT NULL").
		Count(&remaining).Error)
	require.Equal(t, int64(1), remaining)
}

func requireWebhookDeliveryDeleted(t *testing.T, db *gorm.DB, deliveryID string) {
	t.Helper()
	var count int64
	require.NoError(t, db.Model(&database.WebhookDelivery{}).Where("delivery_id = ?", deliveryID).Count(&count).Error)
	require.Zero(t, count)
}

func requireWebhookDeliveryPresent(t *testing.T, db *gorm.DB, deliveryID string) {
	t.Helper()
	var delivery database.WebhookDelivery
	require.NoError(t, db.Where("delivery_id = ?", deliveryID).First(&delivery).Error)
}
