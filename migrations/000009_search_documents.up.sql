CREATE TABLE IF NOT EXISTS search_documents (
    id BIGSERIAL PRIMARY KEY,
    repository_id BIGINT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    document_type TEXT NOT NULL,
    document_github_id BIGINT NOT NULL,
    number INTEGER NOT NULL,
    state TEXT NOT NULL DEFAULT '',
    author_id BIGINT NULL REFERENCES users(id) ON DELETE SET NULL,
    author_login TEXT NOT NULL DEFAULT '',
    api_url TEXT NOT NULL DEFAULT '',
    html_url TEXT NOT NULL DEFAULT '',
    title_text TEXT NOT NULL DEFAULT '',
    body_text TEXT NOT NULL DEFAULT '',
    search_text TEXT NOT NULL DEFAULT '',
    normalized_text TEXT NOT NULL DEFAULT '',
    object_created_at TIMESTAMPTZ NOT NULL,
    object_updated_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT idx_search_documents_repo_type_github UNIQUE (repository_id, document_type, document_github_id)
);

CREATE INDEX IF NOT EXISTS idx_search_documents_repo_type
    ON search_documents (repository_id, document_type);

CREATE INDEX IF NOT EXISTS idx_search_documents_repo_state
    ON search_documents (repository_id, state);

CREATE INDEX IF NOT EXISTS idx_search_documents_repo_author
    ON search_documents (repository_id, author_login);

CREATE INDEX IF NOT EXISTS idx_search_documents_repo_number
    ON search_documents (repository_id, number);

CREATE INDEX IF NOT EXISTS idx_search_documents_fts
    ON search_documents
    USING GIN (to_tsvector('simple', search_text));
