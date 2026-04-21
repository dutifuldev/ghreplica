DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM pg_class
        WHERE relname = 'idx_webhook_deliveries_processed_at_legacy'
    ) AND NOT EXISTS (
        SELECT 1
        FROM pg_class
        WHERE relname = 'idx_webhook_deliveries_processed_at'
    ) THEN
        ALTER INDEX idx_webhook_deliveries_processed_at_legacy
        RENAME TO idx_webhook_deliveries_processed_at;
    END IF;
END
$$;
