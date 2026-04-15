CREATE TABLE IF NOT EXISTS git_refs (
    id BIGSERIAL PRIMARY KEY,
    repository_id BIGINT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    ref_name TEXT NOT NULL,
    target_oid TEXT NOT NULL DEFAULT '',
    target_type TEXT NOT NULL DEFAULT '',
    peeled_commit_sha TEXT NOT NULL DEFAULT '',
    is_symbolic BOOLEAN NOT NULL DEFAULT FALSE,
    symbolic_target TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT idx_git_refs_repo_name UNIQUE (repository_id, ref_name)
);

CREATE TABLE IF NOT EXISTS git_commits (
    id BIGSERIAL PRIMARY KEY,
    repository_id BIGINT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    sha TEXT NOT NULL,
    tree_sha TEXT NOT NULL DEFAULT '',
    author_name TEXT NOT NULL DEFAULT '',
    author_email TEXT NOT NULL DEFAULT '',
    authored_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    authored_timezone_offset INTEGER NOT NULL DEFAULT 0,
    committer_name TEXT NOT NULL DEFAULT '',
    committer_email TEXT NOT NULL DEFAULT '',
    committed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    committed_timezone_offset INTEGER NOT NULL DEFAULT 0,
    message TEXT NOT NULL DEFAULT '',
    message_encoding TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT idx_git_commits_repo_sha UNIQUE (repository_id, sha)
);

CREATE TABLE IF NOT EXISTS git_commit_parents (
    id BIGSERIAL PRIMARY KEY,
    repository_id BIGINT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    commit_sha TEXT NOT NULL,
    parent_sha TEXT NOT NULL DEFAULT '',
    parent_index INTEGER NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT idx_git_commit_parents_repo_commit_parent UNIQUE (repository_id, commit_sha, parent_sha, parent_index)
);

CREATE INDEX IF NOT EXISTS idx_git_commit_parents_repo_parent
    ON git_commit_parents(repository_id, parent_sha);

CREATE TABLE IF NOT EXISTS git_commit_parent_files (
    id BIGSERIAL PRIMARY KEY,
    repository_id BIGINT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    commit_sha TEXT NOT NULL,
    parent_sha TEXT NOT NULL DEFAULT '',
    parent_index INTEGER NOT NULL,
    path TEXT NOT NULL,
    previous_path TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '',
    file_kind TEXT NOT NULL DEFAULT '',
    indexed_as TEXT NOT NULL DEFAULT '',
    old_mode TEXT NOT NULL DEFAULT '',
    new_mode TEXT NOT NULL DEFAULT '',
    blob_sha TEXT NOT NULL DEFAULT '',
    previous_blob_sha TEXT NOT NULL DEFAULT '',
    additions INTEGER NOT NULL DEFAULT 0,
    deletions INTEGER NOT NULL DEFAULT 0,
    changes INTEGER NOT NULL DEFAULT 0,
    patch_text TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT idx_git_commit_parent_files_unique UNIQUE (repository_id, commit_sha, parent_index, path)
);

CREATE INDEX IF NOT EXISTS idx_git_commit_parent_files_repo_path
    ON git_commit_parent_files(repository_id, path);

CREATE TABLE IF NOT EXISTS git_commit_parent_hunks (
    id BIGSERIAL PRIMARY KEY,
    repository_id BIGINT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    commit_sha TEXT NOT NULL,
    parent_sha TEXT NOT NULL DEFAULT '',
    parent_index INTEGER NOT NULL,
    path TEXT NOT NULL,
    hunk_index INTEGER NOT NULL,
    diff_hunk TEXT NOT NULL DEFAULT '',
    old_start INTEGER NOT NULL DEFAULT 0,
    old_count INTEGER NOT NULL DEFAULT 0,
    old_end INTEGER NOT NULL DEFAULT 0,
    new_start INTEGER NOT NULL DEFAULT 0,
    new_count INTEGER NOT NULL DEFAULT 0,
    new_end INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT idx_git_commit_parent_hunks_unique UNIQUE (repository_id, commit_sha, parent_index, path, hunk_index)
);

CREATE INDEX IF NOT EXISTS idx_git_commit_parent_hunks_repo_path
    ON git_commit_parent_hunks(repository_id, path);

CREATE TABLE IF NOT EXISTS pull_request_change_snapshots (
    id BIGSERIAL PRIMARY KEY,
    repository_id BIGINT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    pull_request_id BIGINT NOT NULL REFERENCES pull_requests(issue_id) ON DELETE CASCADE,
    pull_request_number INTEGER NOT NULL,
    head_sha TEXT NOT NULL DEFAULT '',
    base_sha TEXT NOT NULL DEFAULT '',
    merge_base_sha TEXT NOT NULL DEFAULT '',
    base_ref TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL DEFAULT '',
    draft BOOLEAN NOT NULL DEFAULT FALSE,
    indexed_as TEXT NOT NULL DEFAULT '',
    index_freshness TEXT NOT NULL DEFAULT '',
    path_count INTEGER NOT NULL DEFAULT 0,
    indexed_file_count INTEGER NOT NULL DEFAULT 0,
    hunk_count INTEGER NOT NULL DEFAULT 0,
    additions INTEGER NOT NULL DEFAULT 0,
    deletions INTEGER NOT NULL DEFAULT 0,
    patch_bytes INTEGER NOT NULL DEFAULT 0,
    last_indexed_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT idx_pr_change_snapshots_repo_pr UNIQUE (repository_id, pull_request_number)
);

CREATE INDEX IF NOT EXISTS idx_pr_change_snapshots_state
    ON pull_request_change_snapshots(repository_id, state, draft);

CREATE TABLE IF NOT EXISTS pull_request_change_files (
    id BIGSERIAL PRIMARY KEY,
    snapshot_id BIGINT NOT NULL REFERENCES pull_request_change_snapshots(id) ON DELETE CASCADE,
    repository_id BIGINT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    pull_request_number INTEGER NOT NULL,
    head_sha TEXT NOT NULL DEFAULT '',
    base_sha TEXT NOT NULL DEFAULT '',
    merge_base_sha TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL,
    previous_path TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '',
    file_kind TEXT NOT NULL DEFAULT '',
    indexed_as TEXT NOT NULL DEFAULT '',
    old_mode TEXT NOT NULL DEFAULT '',
    new_mode TEXT NOT NULL DEFAULT '',
    head_blob_sha TEXT NOT NULL DEFAULT '',
    base_blob_sha TEXT NOT NULL DEFAULT '',
    additions INTEGER NOT NULL DEFAULT 0,
    deletions INTEGER NOT NULL DEFAULT 0,
    changes INTEGER NOT NULL DEFAULT 0,
    patch_text TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT idx_pr_change_files_snapshot_path UNIQUE (snapshot_id, path)
);

CREATE INDEX IF NOT EXISTS idx_pr_change_files_repo_path
    ON pull_request_change_files(repository_id, path);

CREATE INDEX IF NOT EXISTS idx_pr_change_files_repo_pr
    ON pull_request_change_files(repository_id, pull_request_number);

CREATE INDEX IF NOT EXISTS idx_pr_change_files_head
    ON pull_request_change_files(head_sha);

CREATE TABLE IF NOT EXISTS pull_request_change_hunks (
    id BIGSERIAL PRIMARY KEY,
    snapshot_id BIGINT NOT NULL REFERENCES pull_request_change_snapshots(id) ON DELETE CASCADE,
    repository_id BIGINT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    pull_request_number INTEGER NOT NULL,
    head_sha TEXT NOT NULL DEFAULT '',
    base_sha TEXT NOT NULL DEFAULT '',
    merge_base_sha TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL,
    hunk_index INTEGER NOT NULL,
    diff_hunk TEXT NOT NULL DEFAULT '',
    old_start INTEGER NOT NULL DEFAULT 0,
    old_count INTEGER NOT NULL DEFAULT 0,
    old_end INTEGER NOT NULL DEFAULT 0,
    new_start INTEGER NOT NULL DEFAULT 0,
    new_count INTEGER NOT NULL DEFAULT 0,
    new_end INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT idx_pr_change_hunks_unique UNIQUE (snapshot_id, path, hunk_index)
);

CREATE INDEX IF NOT EXISTS idx_pr_change_hunks_repo_path
    ON pull_request_change_hunks(repository_id, path);

CREATE INDEX IF NOT EXISTS idx_pr_change_hunks_repo_pr
    ON pull_request_change_hunks(repository_id, pull_request_number);

CREATE INDEX IF NOT EXISTS idx_pr_change_hunks_head
    ON pull_request_change_hunks(head_sha);
