DROP INDEX IF EXISTS idx_repo_targeted_pull_refreshes_parked_at;
DROP INDEX IF EXISTS idx_repo_targeted_pull_refreshes_next_attempt_at;

ALTER TABLE repo_targeted_pull_refreshes
    DROP COLUMN IF EXISTS parked_at;

ALTER TABLE repo_targeted_pull_refreshes
    DROP COLUMN IF EXISTS next_attempt_at;

ALTER TABLE repo_targeted_pull_refreshes
    DROP COLUMN IF EXISTS attempt_count;
