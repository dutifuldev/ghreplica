ALTER TABLE tracked_repositories
    ADD COLUMN IF NOT EXISTS webhook_projection_enabled BOOLEAN NOT NULL DEFAULT TRUE;

ALTER TABLE tracked_repositories
    ADD COLUMN IF NOT EXISTS allow_manual_backfill BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE tracked_repositories
    ADD COLUMN IF NOT EXISTS issues_completeness TEXT NOT NULL DEFAULT 'empty';

ALTER TABLE tracked_repositories
    ADD COLUMN IF NOT EXISTS pulls_completeness TEXT NOT NULL DEFAULT 'empty';

ALTER TABLE tracked_repositories
    ADD COLUMN IF NOT EXISTS comments_completeness TEXT NOT NULL DEFAULT 'empty';

ALTER TABLE tracked_repositories
    ADD COLUMN IF NOT EXISTS reviews_completeness TEXT NOT NULL DEFAULT 'empty';

UPDATE tracked_repositories
SET sync_mode = CASE
    WHEN sync_mode = 'webhook' THEN 'webhook_only'
    WHEN sync_mode = 'poll' THEN 'manual_backfill'
    WHEN sync_mode = '' THEN 'manual_backfill'
    ELSE sync_mode
END
WHERE sync_mode IN ('webhook', 'poll', '');

UPDATE tracked_repositories
SET
    webhook_projection_enabled = COALESCE(webhook_projection_enabled, TRUE),
    allow_manual_backfill = CASE
        WHEN sync_mode = 'webhook_only' THEN FALSE
        ELSE TRUE
    END;

ALTER TABLE repository_refresh_jobs
    ADD COLUMN IF NOT EXISTS job_type TEXT NOT NULL DEFAULT 'bootstrap_repository';

ALTER TABLE repository_refresh_jobs
    ADD COLUMN IF NOT EXISTS lease_expires_at TIMESTAMPTZ NULL;

CREATE INDEX IF NOT EXISTS idx_repository_refresh_jobs_status_lease
    ON repository_refresh_jobs(status, lease_expires_at, requested_at, id);

UPDATE repository_refresh_jobs
SET
    status = 'superseded',
    last_error = CASE
        WHEN last_error = '' THEN 'superseded by direct webhook projection'
        ELSE last_error
    END,
    finished_at = COALESCE(finished_at, NOW()),
    updated_at = NOW(),
    next_attempt_at = NULL,
    lease_expires_at = NULL
WHERE source = 'webhook' AND status IN ('pending', 'processing', 'failed');
