DROP INDEX IF EXISTS idx_repository_refresh_jobs_status_lease;

ALTER TABLE repository_refresh_jobs
    DROP COLUMN IF EXISTS lease_expires_at;

ALTER TABLE repository_refresh_jobs
    DROP COLUMN IF EXISTS job_type;

ALTER TABLE tracked_repositories
    DROP COLUMN IF EXISTS reviews_completeness;

ALTER TABLE tracked_repositories
    DROP COLUMN IF EXISTS comments_completeness;

ALTER TABLE tracked_repositories
    DROP COLUMN IF EXISTS pulls_completeness;

ALTER TABLE tracked_repositories
    DROP COLUMN IF EXISTS issues_completeness;

ALTER TABLE tracked_repositories
    DROP COLUMN IF EXISTS allow_manual_backfill;

ALTER TABLE tracked_repositories
    DROP COLUMN IF EXISTS webhook_projection_enabled;
