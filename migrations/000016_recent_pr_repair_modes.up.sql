ALTER TABLE repo_change_sync_states
    ADD COLUMN IF NOT EXISTS last_recent_pr_repair_requested_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_recent_pr_repair_started_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_recent_pr_repair_finished_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_successful_recent_pr_repair_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS recent_pr_repair_cursor_page INTEGER NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS recent_pr_repair_lease_owner_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS recent_pr_repair_lease_started_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS recent_pr_repair_lease_heartbeat_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS recent_pr_repair_lease_until TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS full_history_cursor_page INTEGER NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS last_full_history_repair_started_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_full_history_repair_finished_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_successful_full_history_repair_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS full_history_repair_lease_owner_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS full_history_repair_lease_started_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS full_history_repair_lease_heartbeat_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS full_history_repair_lease_until TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_repo_change_sync_states_recent_pr_repair_lease_heartbeat
    ON repo_change_sync_states(recent_pr_repair_lease_heartbeat_at);

CREATE INDEX IF NOT EXISTS idx_repo_change_sync_states_recent_pr_repair_lease_until
    ON repo_change_sync_states(recent_pr_repair_lease_until);

CREATE INDEX IF NOT EXISTS idx_repo_change_sync_states_last_recent_pr_repair_requested
    ON repo_change_sync_states(last_recent_pr_repair_requested_at);

CREATE INDEX IF NOT EXISTS idx_repo_change_sync_states_full_history_repair_lease_heartbeat
    ON repo_change_sync_states(full_history_repair_lease_heartbeat_at);

CREATE INDEX IF NOT EXISTS idx_repo_change_sync_states_full_history_repair_lease_until
    ON repo_change_sync_states(full_history_repair_lease_until);
