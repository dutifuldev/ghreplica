DROP INDEX IF EXISTS idx_repo_change_sync_states_backfill_lease_heartbeat_at;
DROP INDEX IF EXISTS idx_repo_change_sync_states_backfill_lease_owner_id;
DROP INDEX IF EXISTS idx_repo_change_sync_states_fetch_lease_heartbeat_at;
DROP INDEX IF EXISTS idx_repo_change_sync_states_fetch_lease_owner_id;

ALTER TABLE repo_change_sync_states
    DROP COLUMN IF EXISTS backfill_lease_heartbeat_at;

ALTER TABLE repo_change_sync_states
    DROP COLUMN IF EXISTS backfill_lease_started_at;

ALTER TABLE repo_change_sync_states
    DROP COLUMN IF EXISTS backfill_lease_owner_id;

ALTER TABLE repo_change_sync_states
    DROP COLUMN IF EXISTS fetch_lease_heartbeat_at;

ALTER TABLE repo_change_sync_states
    DROP COLUMN IF EXISTS fetch_lease_started_at;

ALTER TABLE repo_change_sync_states
    DROP COLUMN IF EXISTS fetch_lease_owner_id;
