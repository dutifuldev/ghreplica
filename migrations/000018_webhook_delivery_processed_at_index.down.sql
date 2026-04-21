DROP INDEX IF EXISTS idx_webhook_deliveries_processed_at;

ALTER TABLE webhook_deliveries
DROP COLUMN IF EXISTS compacted_at;
