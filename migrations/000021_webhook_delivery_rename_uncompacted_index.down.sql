DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM pg_class
        WHERE relname = 'idx_webhook_deliveries_processed_at'
    ) AND NOT EXISTS (
        SELECT 1
        FROM pg_class
        WHERE relname = 'idx_webhook_deliveries_processed_at_uncompacted'
    ) THEN
        ALTER INDEX idx_webhook_deliveries_processed_at
        RENAME TO idx_webhook_deliveries_processed_at_uncompacted;
    END IF;
END
$$;
