ALTER TABLE repo_change_sync_states
    ADD COLUMN IF NOT EXISTS fetch_lease_owner_id TEXT NOT NULL DEFAULT '';

ALTER TABLE repo_change_sync_states
    ADD COLUMN IF NOT EXISTS fetch_lease_started_at TIMESTAMPTZ;

ALTER TABLE repo_change_sync_states
    ADD COLUMN IF NOT EXISTS fetch_lease_heartbeat_at TIMESTAMPTZ;

ALTER TABLE repo_change_sync_states
    ADD COLUMN IF NOT EXISTS backfill_lease_owner_id TEXT NOT NULL DEFAULT '';

ALTER TABLE repo_change_sync_states
    ADD COLUMN IF NOT EXISTS backfill_lease_started_at TIMESTAMPTZ;

ALTER TABLE repo_change_sync_states
    ADD COLUMN IF NOT EXISTS backfill_lease_heartbeat_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_repo_change_sync_states_fetch_lease_owner_id
    ON repo_change_sync_states(fetch_lease_owner_id);

CREATE INDEX IF NOT EXISTS idx_repo_change_sync_states_fetch_lease_heartbeat_at
    ON repo_change_sync_states(fetch_lease_heartbeat_at);

CREATE INDEX IF NOT EXISTS idx_repo_change_sync_states_backfill_lease_owner_id
    ON repo_change_sync_states(backfill_lease_owner_id);

CREATE INDEX IF NOT EXISTS idx_repo_change_sync_states_backfill_lease_heartbeat_at
    ON repo_change_sync_states(backfill_lease_heartbeat_at);
