DROP INDEX IF EXISTS idx_repository_refresh_jobs_status_next_attempt;
ALTER TABLE repository_refresh_jobs DROP COLUMN IF EXISTS next_attempt_at;
ALTER TABLE repository_refresh_jobs DROP COLUMN IF EXISTS max_attempts;
DROP TABLE IF EXISTS pull_request_review_comments;
DROP TABLE IF EXISTS pull_request_reviews;
DROP TABLE IF EXISTS issue_comments;
