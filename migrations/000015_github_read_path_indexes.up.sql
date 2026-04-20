CREATE INDEX IF NOT EXISTS idx_repositories_owner_login_name
    ON repositories(owner_login, name);

CREATE INDEX IF NOT EXISTS idx_issues_repo_created_at
    ON issues(repository_id, github_created_at DESC);

CREATE INDEX IF NOT EXISTS idx_issues_repo_state_created_at
    ON issues(repository_id, state, github_created_at DESC);

CREATE INDEX IF NOT EXISTS idx_pull_requests_repo_created_at
    ON pull_requests(repository_id, github_created_at DESC);

CREATE INDEX IF NOT EXISTS idx_pull_requests_repo_state_created_at
    ON pull_requests(repository_id, state, github_created_at DESC);
