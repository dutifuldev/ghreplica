CREATE TABLE IF NOT EXISTS repo_change_sync_states (
    id BIGSERIAL PRIMARY KEY,
    repository_id BIGINT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    dirty BOOLEAN NOT NULL DEFAULT FALSE,
    dirty_since TIMESTAMPTZ NULL,
    last_webhook_at TIMESTAMPTZ NULL,
    last_requested_fetch_at TIMESTAMPTZ NULL,
    last_fetch_started_at TIMESTAMPTZ NULL,
    last_fetch_finished_at TIMESTAMPTZ NULL,
    last_successful_fetch_at TIMESTAMPTZ NULL,
    last_backfill_started_at TIMESTAMPTZ NULL,
    last_backfill_finished_at TIMESTAMPTZ NULL,
    last_open_pr_scan_at TIMESTAMPTZ NULL,
    open_pr_total INTEGER NOT NULL DEFAULT 0,
    open_pr_current INTEGER NOT NULL DEFAULT 0,
    open_pr_stale INTEGER NOT NULL DEFAULT 0,
    open_pr_cursor_number INTEGER NULL,
    open_pr_cursor_updated_at TIMESTAMPTZ NULL,
    backfill_mode TEXT NOT NULL DEFAULT 'off',
    backfill_priority INTEGER NOT NULL DEFAULT 0,
    fetch_lease_until TIMESTAMPTZ NULL,
    backfill_lease_until TIMESTAMPTZ NULL,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT idx_repo_change_sync_states_repo UNIQUE (repository_id)
);

CREATE INDEX IF NOT EXISTS idx_repo_change_sync_states_dirty
    ON repo_change_sync_states(dirty);

CREATE INDEX IF NOT EXISTS idx_repo_change_sync_states_backfill_mode
    ON repo_change_sync_states(backfill_mode);

CREATE INDEX IF NOT EXISTS idx_repo_change_sync_states_fetch_lease
    ON repo_change_sync_states(fetch_lease_until);

CREATE INDEX IF NOT EXISTS idx_repo_change_sync_states_backfill_lease
    ON repo_change_sync_states(backfill_lease_until);
