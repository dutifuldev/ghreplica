CREATE TABLE IF NOT EXISTS repo_open_pull_inventories (
    id BIGSERIAL PRIMARY KEY,
    repository_id BIGINT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    pull_request_number INTEGER NOT NULL,
    github_updated_at TIMESTAMPTZ NOT NULL,
    head_sha TEXT NOT NULL DEFAULT '',
    base_sha TEXT NOT NULL DEFAULT '',
    base_ref TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL DEFAULT '',
    draft BOOLEAN NOT NULL DEFAULT FALSE,
    freshness_state TEXT NOT NULL DEFAULT '',
    last_seen_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT idx_repo_open_pull_inventories_repo_pr UNIQUE (repository_id, pull_request_number)
);

CREATE INDEX IF NOT EXISTS idx_repo_open_pull_inventories_repo_freshness_updated
    ON repo_open_pull_inventories(repository_id, freshness_state, github_updated_at DESC, pull_request_number DESC);
