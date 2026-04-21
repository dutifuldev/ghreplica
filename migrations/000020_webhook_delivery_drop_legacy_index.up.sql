DO $$
DECLARE
    predicate TEXT;
BEGIN
    SELECT pg_get_expr(i.indpred, i.indrelid)
    INTO predicate
    FROM pg_class c
    JOIN pg_index i ON i.indexrelid = c.oid
    WHERE c.relname = 'idx_webhook_deliveries_processed_at';

    IF predicate IS NOT NULL
        AND predicate LIKE '%processed_at IS NOT NULL%'
        AND predicate NOT LIKE '%compacted_at IS NULL%' THEN
        ALTER INDEX idx_webhook_deliveries_processed_at
        RENAME TO idx_webhook_deliveries_processed_at_legacy;
    END IF;
END
$$;
