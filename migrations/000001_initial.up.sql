CREATE TABLE IF NOT EXISTS schema_migrations (
    version TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS tracked_repositories (
    id BIGSERIAL PRIMARY KEY,
    owner TEXT NOT NULL,
    name TEXT NOT NULL,
    full_name TEXT NOT NULL UNIQUE,
    repository_id BIGINT NULL,
    sync_mode TEXT NOT NULL DEFAULT 'poll',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    last_bootstrap_at TIMESTAMPTZ NULL,
    last_crawl_at TIMESTAMPTZ NULL,
    last_webhook_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS users (
    id BIGSERIAL PRIMARY KEY,
    github_id BIGINT NOT NULL UNIQUE,
    node_id TEXT NOT NULL DEFAULT '',
    login TEXT NOT NULL DEFAULT '',
    type TEXT NOT NULL DEFAULT '',
    site_admin BOOLEAN NOT NULL DEFAULT FALSE,
    name TEXT NOT NULL DEFAULT '',
    avatar_url TEXT NOT NULL DEFAULT '',
    html_url TEXT NOT NULL DEFAULT '',
    api_url TEXT NOT NULL DEFAULT '',
    raw_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_users_login ON users(login);

CREATE TABLE IF NOT EXISTS repositories (
    id BIGSERIAL PRIMARY KEY,
    github_id BIGINT NOT NULL UNIQUE,
    node_id TEXT NOT NULL DEFAULT '',
    owner_id BIGINT NULL REFERENCES users(id),
    owner_login TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL,
    full_name TEXT NOT NULL UNIQUE,
    private BOOLEAN NOT NULL DEFAULT FALSE,
    archived BOOLEAN NOT NULL DEFAULT FALSE,
    disabled BOOLEAN NOT NULL DEFAULT FALSE,
    default_branch TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    html_url TEXT NOT NULL DEFAULT '',
    api_url TEXT NOT NULL DEFAULT '',
    visibility TEXT NOT NULL DEFAULT '',
    fork BOOLEAN NOT NULL DEFAULT FALSE,
    raw_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS issues (
    id BIGSERIAL PRIMARY KEY,
    repository_id BIGINT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    github_id BIGINT NOT NULL,
    node_id TEXT NOT NULL DEFAULT '',
    number INTEGER NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    body TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL DEFAULT '',
    state_reason TEXT NOT NULL DEFAULT '',
    author_id BIGINT NULL REFERENCES users(id),
    comments_count INTEGER NOT NULL DEFAULT 0,
    locked BOOLEAN NOT NULL DEFAULT FALSE,
    is_pull_request BOOLEAN NOT NULL DEFAULT FALSE,
    pull_request_api_url TEXT NOT NULL DEFAULT '',
    html_url TEXT NOT NULL DEFAULT '',
    api_url TEXT NOT NULL DEFAULT '',
    github_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    github_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    closed_at TIMESTAMPTZ NULL,
    raw_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT idx_repo_issue_number UNIQUE (repository_id, number)
);

CREATE INDEX IF NOT EXISTS idx_issues_state_updated ON issues(repository_id, state, github_updated_at);

CREATE TABLE IF NOT EXISTS pull_requests (
    issue_id BIGINT PRIMARY KEY REFERENCES issues(id) ON DELETE CASCADE,
    repository_id BIGINT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    github_id BIGINT NOT NULL,
    node_id TEXT NOT NULL DEFAULT '',
    number INTEGER NOT NULL,
    state TEXT NOT NULL DEFAULT '',
    draft BOOLEAN NOT NULL DEFAULT FALSE,
    head_repo_id BIGINT NULL REFERENCES repositories(id),
    head_ref TEXT NOT NULL DEFAULT '',
    head_sha TEXT NOT NULL DEFAULT '',
    base_repo_id BIGINT NULL REFERENCES repositories(id),
    base_ref TEXT NOT NULL DEFAULT '',
    base_sha TEXT NOT NULL DEFAULT '',
    mergeable BOOLEAN NULL,
    mergeable_state TEXT NOT NULL DEFAULT '',
    merged BOOLEAN NOT NULL DEFAULT FALSE,
    merged_at TIMESTAMPTZ NULL,
    merged_by_id BIGINT NULL REFERENCES users(id),
    merge_commit_sha TEXT NOT NULL DEFAULT '',
    additions INTEGER NOT NULL DEFAULT 0,
    deletions INTEGER NOT NULL DEFAULT 0,
    changed_files INTEGER NOT NULL DEFAULT 0,
    commits_count INTEGER NOT NULL DEFAULT 0,
    html_url TEXT NOT NULL DEFAULT '',
    api_url TEXT NOT NULL DEFAULT '',
    diff_url TEXT NOT NULL DEFAULT '',
    patch_url TEXT NOT NULL DEFAULT '',
    github_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    github_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    raw_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_pull_requests_repo_number ON pull_requests(repository_id, number);
CREATE INDEX IF NOT EXISTS idx_pull_requests_head_sha ON pull_requests(head_sha);

CREATE TABLE IF NOT EXISTS webhook_deliveries (
    id BIGSERIAL PRIMARY KEY,
    delivery_id TEXT NOT NULL UNIQUE,
    event TEXT NOT NULL DEFAULT '',
    action TEXT NOT NULL DEFAULT '',
    repository_id BIGINT NULL REFERENCES repositories(id),
    headers_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    payload_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at TIMESTAMPTZ NULL
);
