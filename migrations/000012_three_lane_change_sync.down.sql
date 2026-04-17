DROP TABLE IF EXISTS repo_targeted_pull_refreshes;

DROP INDEX IF EXISTS idx_repo_open_pull_inventories_repo_gen_freshness_updated;
DROP INDEX IF EXISTS idx_repo_open_pull_inventories_repo_gen_pr;

ALTER TABLE repo_open_pull_inventories
    DROP COLUMN IF EXISTS generation;

CREATE UNIQUE INDEX IF NOT EXISTS idx_repo_open_pull_inventories_repo_pr
    ON repo_open_pull_inventories(repository_id, pull_request_number);

CREATE INDEX IF NOT EXISTS idx_repo_open_pull_inventories_repo_freshness_updated
    ON repo_open_pull_inventories(repository_id, freshness_state, github_updated_at DESC, pull_request_number DESC);

ALTER TABLE repo_change_sync_states
    DROP COLUMN IF EXISTS inventory_generation_current;

ALTER TABLE repo_change_sync_states
    DROP COLUMN IF EXISTS inventory_generation_building;

ALTER TABLE repo_change_sync_states
    DROP COLUMN IF EXISTS inventory_last_committed_at;

ALTER TABLE repo_change_sync_states
    DROP COLUMN IF EXISTS backfill_generation;
