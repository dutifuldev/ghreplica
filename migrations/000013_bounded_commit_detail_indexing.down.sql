ALTER TABLE git_commit_parents
    DROP COLUMN IF EXISTS indexed_as;

ALTER TABLE git_commit_parents
    DROP COLUMN IF EXISTS index_reason;

ALTER TABLE git_commit_parents
    DROP COLUMN IF EXISTS path_count;

ALTER TABLE git_commit_parents
    DROP COLUMN IF EXISTS hunk_count;

ALTER TABLE git_commit_parents
    DROP COLUMN IF EXISTS additions;

ALTER TABLE git_commit_parents
    DROP COLUMN IF EXISTS deletions;

ALTER TABLE git_commit_parents
    DROP COLUMN IF EXISTS patch_bytes;

ALTER TABLE git_commit_parents
    DROP COLUMN IF EXISTS last_indexed_at;
