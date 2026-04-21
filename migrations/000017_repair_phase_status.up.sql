ALTER TABLE repo_change_sync_states
    ADD COLUMN IF NOT EXISTS last_recent_pr_repair_error TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS last_full_history_repair_error TEXT NOT NULL DEFAULT '';
