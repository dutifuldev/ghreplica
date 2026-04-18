ALTER TABLE git_commit_parents
    ADD COLUMN IF NOT EXISTS indexed_as TEXT NOT NULL DEFAULT 'full';

ALTER TABLE git_commit_parents
    ADD COLUMN IF NOT EXISTS index_reason TEXT NOT NULL DEFAULT '';

ALTER TABLE git_commit_parents
    ADD COLUMN IF NOT EXISTS path_count INTEGER NOT NULL DEFAULT 0;

ALTER TABLE git_commit_parents
    ADD COLUMN IF NOT EXISTS hunk_count INTEGER NOT NULL DEFAULT 0;

ALTER TABLE git_commit_parents
    ADD COLUMN IF NOT EXISTS additions INTEGER NOT NULL DEFAULT 0;

ALTER TABLE git_commit_parents
    ADD COLUMN IF NOT EXISTS deletions INTEGER NOT NULL DEFAULT 0;

ALTER TABLE git_commit_parents
    ADD COLUMN IF NOT EXISTS patch_bytes INTEGER NOT NULL DEFAULT 0;

ALTER TABLE git_commit_parents
    ADD COLUMN IF NOT EXISTS last_indexed_at TIMESTAMPTZ;

UPDATE git_commit_parents
SET indexed_as = COALESCE(NULLIF(indexed_as, ''), 'full'),
    index_reason = COALESCE(index_reason, ''),
    path_count = COALESCE(path_count, 0),
    hunk_count = COALESCE(hunk_count, 0),
    additions = COALESCE(additions, 0),
    deletions = COALESCE(deletions, 0),
    patch_bytes = COALESCE(patch_bytes, 0)
WHERE indexed_as = ''
   OR index_reason IS NULL
   OR path_count IS NULL
   OR hunk_count IS NULL
   OR additions IS NULL
   OR deletions IS NULL
   OR patch_bytes IS NULL;
