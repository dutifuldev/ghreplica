ALTER TABLE repo_change_sync_states
    ADD COLUMN IF NOT EXISTS inventory_generation_current INTEGER NOT NULL DEFAULT 0;

ALTER TABLE repo_change_sync_states
    ADD COLUMN IF NOT EXISTS inventory_generation_building INTEGER;

ALTER TABLE repo_change_sync_states
    ADD COLUMN IF NOT EXISTS inventory_last_committed_at TIMESTAMPTZ;

ALTER TABLE repo_change_sync_states
    ADD COLUMN IF NOT EXISTS backfill_generation INTEGER NOT NULL DEFAULT 0;

ALTER TABLE repo_open_pull_inventories
    ADD COLUMN IF NOT EXISTS generation INTEGER NOT NULL DEFAULT 1;

UPDATE repo_change_sync_states
SET inventory_generation_current = CASE
        WHEN EXISTS (
            SELECT 1
            FROM repo_open_pull_inventories inv
            WHERE inv.repository_id = repo_change_sync_states.repository_id
        ) THEN 1
        ELSE 0
    END,
    backfill_generation = CASE
        WHEN EXISTS (
            SELECT 1
            FROM repo_open_pull_inventories inv
            WHERE inv.repository_id = repo_change_sync_states.repository_id
        ) THEN 1
        ELSE 0
    END,
    inventory_last_committed_at = COALESCE(inventory_last_committed_at, last_open_pr_scan_at)
WHERE inventory_generation_current = 0
   OR backfill_generation = 0
   OR inventory_last_committed_at IS NULL;

DROP INDEX IF EXISTS idx_repo_open_pull_inventories_repo_freshness_updated;

ALTER TABLE repo_open_pull_inventories
    DROP CONSTRAINT IF EXISTS idx_repo_open_pull_inventories_repo_pr;

CREATE UNIQUE INDEX IF NOT EXISTS idx_repo_open_pull_inventories_repo_gen_pr
    ON repo_open_pull_inventories(repository_id, generation, pull_request_number);

CREATE INDEX IF NOT EXISTS idx_repo_open_pull_inventories_repo_gen_freshness_updated
    ON repo_open_pull_inventories(repository_id, generation, freshness_state, github_updated_at DESC, pull_request_number DESC);

CREATE TABLE IF NOT EXISTS repo_targeted_pull_refreshes (
    id BIGSERIAL PRIMARY KEY,
    repository_id BIGINT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    pull_request_number INTEGER NOT NULL,
    requested_at TIMESTAMPTZ NULL,
    last_webhook_at TIMESTAMPTZ NULL,
    last_attempted_at TIMESTAMPTZ NULL,
    last_completed_at TIMESTAMPTZ NULL,
    lease_owner_id TEXT NOT NULL DEFAULT '',
    lease_started_at TIMESTAMPTZ NULL,
    lease_heartbeat_at TIMESTAMPTZ NULL,
    lease_until TIMESTAMPTZ NULL,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT idx_repo_targeted_pull_refreshes_repo_pr UNIQUE (repository_id, pull_request_number)
);

CREATE INDEX IF NOT EXISTS idx_repo_targeted_pull_refreshes_repo_requested
    ON repo_targeted_pull_refreshes(repository_id, requested_at, pull_request_number);

CREATE INDEX IF NOT EXISTS idx_repo_targeted_pull_refreshes_lease_owner_id
    ON repo_targeted_pull_refreshes(lease_owner_id);

CREATE INDEX IF NOT EXISTS idx_repo_targeted_pull_refreshes_lease_heartbeat_at
    ON repo_targeted_pull_refreshes(lease_heartbeat_at);
