DROP INDEX IF EXISTS idx_repo_change_sync_states_targeted_refresh_lease_until;
DROP INDEX IF EXISTS idx_repo_change_sync_states_targeted_refresh_lease_heartbeat_at;

ALTER TABLE repo_change_sync_states
    DROP COLUMN IF EXISTS targeted_refresh_lease_until;

ALTER TABLE repo_change_sync_states
    DROP COLUMN IF EXISTS targeted_refresh_lease_heartbeat_at;

ALTER TABLE repo_change_sync_states
    DROP COLUMN IF EXISTS targeted_refresh_pending;
