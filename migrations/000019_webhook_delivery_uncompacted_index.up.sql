-- ghreplica:nontransactional

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_webhook_deliveries_processed_at_uncompacted
ON webhook_deliveries (processed_at)
WHERE processed_at IS NOT NULL AND compacted_at IS NULL;

DROP INDEX CONCURRENTLY IF EXISTS idx_webhook_deliveries_processed_at;

ALTER INDEX IF EXISTS idx_webhook_deliveries_processed_at_uncompacted
RENAME TO idx_webhook_deliveries_processed_at;
