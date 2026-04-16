CREATE INDEX IF NOT EXISTS idx_tracked_repositories_repository_id
    ON tracked_repositories(repository_id);

CREATE INDEX IF NOT EXISTS idx_repository_refresh_jobs_repository_id_status
    ON repository_refresh_jobs(repository_id, job_type, status, requested_at, id);
