CREATE TABLE IF NOT EXISTS repository_refresh_jobs (
    id BIGSERIAL PRIMARY KEY,
    tracked_repository_id BIGINT NULL REFERENCES tracked_repositories(id) ON DELETE SET NULL,
    repository_id BIGINT NULL REFERENCES repositories(id) ON DELETE SET NULL,
    owner TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL DEFAULT '',
    full_name TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT '',
    delivery_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    requested_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ NULL,
    finished_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_repository_refresh_jobs_status_requested
    ON repository_refresh_jobs(status, requested_at, id);

CREATE INDEX IF NOT EXISTS idx_repository_refresh_jobs_full_name_status
    ON repository_refresh_jobs(full_name, status);
