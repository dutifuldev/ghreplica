-- ghreplica:nontransactional

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_webhook_deliveries_processed_at
ON webhook_deliveries (processed_at)
WHERE processed_at IS NOT NULL AND compacted_at IS NULL;

DROP INDEX CONCURRENTLY IF EXISTS idx_webhook_deliveries_cleanup;
