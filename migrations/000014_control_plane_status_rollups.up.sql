ALTER TABLE repo_change_sync_states
    ADD COLUMN IF NOT EXISTS targeted_refresh_pending BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE repo_change_sync_states
    ADD COLUMN IF NOT EXISTS targeted_refresh_lease_heartbeat_at TIMESTAMPTZ;

ALTER TABLE repo_change_sync_states
    ADD COLUMN IF NOT EXISTS targeted_refresh_lease_until TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_repo_change_sync_states_targeted_refresh_lease_heartbeat_at
    ON repo_change_sync_states(targeted_refresh_lease_heartbeat_at);

CREATE INDEX IF NOT EXISTS idx_repo_change_sync_states_targeted_refresh_lease_until
    ON repo_change_sync_states(targeted_refresh_lease_until);
