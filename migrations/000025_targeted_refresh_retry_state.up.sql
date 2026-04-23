ALTER TABLE repo_targeted_pull_refreshes
    ADD COLUMN IF NOT EXISTS attempt_count INTEGER NOT NULL DEFAULT 0;

ALTER TABLE repo_targeted_pull_refreshes
    ADD COLUMN IF NOT EXISTS next_attempt_at TIMESTAMPTZ;

ALTER TABLE repo_targeted_pull_refreshes
    ADD COLUMN IF NOT EXISTS parked_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_repo_targeted_pull_refreshes_next_attempt_at
    ON repo_targeted_pull_refreshes(next_attempt_at);

CREATE INDEX IF NOT EXISTS idx_repo_targeted_pull_refreshes_parked_at
    ON repo_targeted_pull_refreshes(parked_at);
