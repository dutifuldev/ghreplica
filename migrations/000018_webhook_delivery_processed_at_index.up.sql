CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_processed_at
ON webhook_deliveries (processed_at)
WHERE processed_at IS NOT NULL;
