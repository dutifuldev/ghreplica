CREATE TABLE IF NOT EXISTS repo_text_search_states (
    repository_id BIGINT PRIMARY KEY REFERENCES repositories(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT '',
    freshness TEXT NOT NULL DEFAULT '',
    coverage TEXT NOT NULL DEFAULT '',
    last_indexed_at TIMESTAMPTZ NULL,
    last_source_update_at TIMESTAMPTZ NULL,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_repo_text_search_states_status
    ON repo_text_search_states (status);
