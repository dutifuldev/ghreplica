-- ghreplica:nontransactional

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_webhook_deliveries_processed_at_legacy
ON webhook_deliveries (processed_at)
WHERE processed_at IS NOT NULL;
