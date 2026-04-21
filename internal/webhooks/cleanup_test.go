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

func TestDeliveryCleanupWorkerDeletesOnlyOldProcessedRows(t *testing.T) {
	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))
	ctx := context.Background()
	now := time.Now().UTC()
	oldProcessedAt := now.Add(-48 * time.Hour)
	recentProcessedAt := now.Add(-2 * time.Hour)

	deliveries := []database.WebhookDelivery{
		{DeliveryID: "old-processed", Event: "ping", HeadersJSON: []byte(`{}`), PayloadJSON: []byte(`{}`), ReceivedAt: oldProcessedAt, ProcessedAt: &oldProcessedAt},
		{DeliveryID: "recent-processed", Event: "ping", HeadersJSON: []byte(`{}`), PayloadJSON: []byte(`{}`), ReceivedAt: recentProcessedAt, ProcessedAt: &recentProcessedAt},
		{DeliveryID: "old-unprocessed", Event: "ping", HeadersJSON: []byte(`{}`), PayloadJSON: []byte(`{}`), ReceivedAt: oldProcessedAt},
	}
	for _, delivery := range deliveries {
		require.NoError(t, db.WithContext(ctx).Create(&delivery).Error)
	}

	worker := webhooks.NewDeliveryCleanupWorker(db, 24*time.Hour, time.Minute, 100)
	processed, err := worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)

	requireWebhookDeliveryMissing(t, db, "old-processed")
	requireWebhookDeliveryPresent(t, db, "recent-processed")
	requireWebhookDeliveryPresent(t, db, "old-unprocessed")
}

func TestDeliveryCleanupWorkerRespectsBatchSize(t *testing.T) {
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
			HeadersJSON: []byte(`{}`),
			PayloadJSON: []byte(`{}`),
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
	require.NoError(t, db.WithContext(ctx).Model(&database.WebhookDelivery{}).Count(&remaining).Error)
	require.Equal(t, int64(1), remaining)
}

func requireWebhookDeliveryMissing(t *testing.T, db *gorm.DB, deliveryID string) {
	t.Helper()
	var delivery database.WebhookDelivery
	err := db.Where("delivery_id = ?", deliveryID).First(&delivery).Error
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func requireWebhookDeliveryPresent(t *testing.T, db *gorm.DB, deliveryID string) {
	t.Helper()
	var delivery database.WebhookDelivery
	require.NoError(t, db.Where("delivery_id = ?", deliveryID).First(&delivery).Error)
}
