ALTER TABLE repo_change_sync_states
    DROP COLUMN IF EXISTS last_full_history_repair_error,
    DROP COLUMN IF EXISTS last_recent_pr_repair_error;
