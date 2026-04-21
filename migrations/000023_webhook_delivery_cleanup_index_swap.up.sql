-- ghreplica:nontransactional

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_webhook_deliveries_cleanup
ON webhook_deliveries (processed_at, id)
WHERE processed_at IS NOT NULL;
