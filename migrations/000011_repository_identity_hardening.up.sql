CREATE INDEX IF NOT EXISTS idx_tracked_repositories_repository_id
    ON tracked_repositories(repository_id);

CREATE INDEX IF NOT EXISTS idx_repository_refresh_jobs_repository_id_status
    ON repository_refresh_jobs(repository_id, job_type, status, requested_at, id);

UPDATE tracked_repositories tr
SET repository_id = r.id
FROM repositories r
WHERE tr.repository_id IS NULL
  AND tr.full_name = r.full_name;

UPDATE repository_refresh_jobs rrj
SET repository_id = r.id
FROM repositories r
WHERE rrj.repository_id IS NULL
  AND rrj.full_name = r.full_name;

UPDATE repository_refresh_jobs rrj
SET tracked_repository_id = tr.id
FROM tracked_repositories tr
WHERE rrj.tracked_repository_id IS NULL
  AND rrj.full_name = tr.full_name;
