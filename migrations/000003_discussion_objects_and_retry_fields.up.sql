CREATE TABLE IF NOT EXISTS issue_comments (
    id BIGSERIAL PRIMARY KEY,
    github_id BIGINT NOT NULL UNIQUE,
    node_id TEXT NOT NULL DEFAULT '',
    repository_id BIGINT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    issue_id BIGINT NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    author_id BIGINT NULL REFERENCES users(id),
    body TEXT NOT NULL DEFAULT '',
    html_url TEXT NOT NULL DEFAULT '',
    api_url TEXT NOT NULL DEFAULT '',
    github_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    github_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    raw_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_issue_comments_issue_id ON issue_comments(issue_id, github_created_at);

CREATE TABLE IF NOT EXISTS pull_request_reviews (
    id BIGSERIAL PRIMARY KEY,
    github_id BIGINT NOT NULL UNIQUE,
    node_id TEXT NOT NULL DEFAULT '',
    repository_id BIGINT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    pull_request_id BIGINT NOT NULL REFERENCES pull_requests(issue_id) ON DELETE CASCADE,
    author_id BIGINT NULL REFERENCES users(id),
    state TEXT NOT NULL DEFAULT '',
    body TEXT NOT NULL DEFAULT '',
    commit_id TEXT NOT NULL DEFAULT '',
    submitted_at TIMESTAMPTZ NULL,
    html_url TEXT NOT NULL DEFAULT '',
    api_url TEXT NOT NULL DEFAULT '',
    github_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    github_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    raw_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_pull_request_reviews_pull_request_id
    ON pull_request_reviews(pull_request_id, github_created_at);

CREATE TABLE IF NOT EXISTS pull_request_review_comments (
    id BIGSERIAL PRIMARY KEY,
    github_id BIGINT NOT NULL UNIQUE,
    node_id TEXT NOT NULL DEFAULT '',
    repository_id BIGINT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    pull_request_id BIGINT NOT NULL REFERENCES pull_requests(issue_id) ON DELETE CASCADE,
    review_id BIGINT NULL REFERENCES pull_request_reviews(id) ON DELETE SET NULL,
    in_reply_to_github_id BIGINT NULL,
    author_id BIGINT NULL REFERENCES users(id),
    path TEXT NOT NULL DEFAULT '',
    diff_hunk TEXT NOT NULL DEFAULT '',
    position INTEGER NULL,
    original_position INTEGER NULL,
    line INTEGER NULL,
    original_line INTEGER NULL,
    side TEXT NOT NULL DEFAULT '',
    body TEXT NOT NULL DEFAULT '',
    html_url TEXT NOT NULL DEFAULT '',
    api_url TEXT NOT NULL DEFAULT '',
    pull_request_url TEXT NOT NULL DEFAULT '',
    github_created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    github_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    raw_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_pull_request_review_comments_pull_request_id
    ON pull_request_review_comments(pull_request_id, github_created_at);

ALTER TABLE repository_refresh_jobs
    ADD COLUMN IF NOT EXISTS max_attempts INTEGER NOT NULL DEFAULT 3;

ALTER TABLE repository_refresh_jobs
    ADD COLUMN IF NOT EXISTS next_attempt_at TIMESTAMPTZ NULL;

CREATE INDEX IF NOT EXISTS idx_repository_refresh_jobs_status_next_attempt
    ON repository_refresh_jobs(status, next_attempt_at, requested_at, id);
