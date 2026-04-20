DROP INDEX IF EXISTS idx_repo_change_sync_states_full_history_repair_lease_until;
DROP INDEX IF EXISTS idx_repo_change_sync_states_full_history_repair_lease_heartbeat;
DROP INDEX IF EXISTS idx_repo_change_sync_states_last_recent_pr_repair_requested;
DROP INDEX IF EXISTS idx_repo_change_sync_states_recent_pr_repair_lease_until;
DROP INDEX IF EXISTS idx_repo_change_sync_states_recent_pr_repair_lease_heartbeat;

ALTER TABLE repo_change_sync_states
    DROP COLUMN IF EXISTS full_history_repair_lease_until,
    DROP COLUMN IF EXISTS full_history_repair_lease_heartbeat_at,
    DROP COLUMN IF EXISTS full_history_repair_lease_started_at,
    DROP COLUMN IF EXISTS full_history_repair_lease_owner_id,
    DROP COLUMN IF EXISTS last_successful_full_history_repair_at,
    DROP COLUMN IF EXISTS last_full_history_repair_finished_at,
    DROP COLUMN IF EXISTS last_full_history_repair_started_at,
    DROP COLUMN IF EXISTS full_history_cursor_page,
    DROP COLUMN IF EXISTS recent_pr_repair_lease_until,
    DROP COLUMN IF EXISTS recent_pr_repair_lease_heartbeat_at,
    DROP COLUMN IF EXISTS recent_pr_repair_lease_started_at,
    DROP COLUMN IF EXISTS recent_pr_repair_lease_owner_id,
    DROP COLUMN IF EXISTS recent_pr_repair_cursor_page,
    DROP COLUMN IF EXISTS last_successful_recent_pr_repair_at,
    DROP COLUMN IF EXISTS last_recent_pr_repair_finished_at,
    DROP COLUMN IF EXISTS last_recent_pr_repair_started_at,
    DROP COLUMN IF EXISTS last_recent_pr_repair_requested_at;
