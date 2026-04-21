package database

func (TrackedRepository) TableName() string         { return "tracked_repositories" }
func (User) TableName() string                      { return "users" }
func (Repository) TableName() string                { return "repositories" }
func (Issue) TableName() string                     { return "issues" }
func (PullRequest) TableName() string               { return "pull_requests" }
func (IssueComment) TableName() string              { return "issue_comments" }
func (PullRequestReview) TableName() string         { return "pull_request_reviews" }
func (PullRequestReviewComment) TableName() string  { return "pull_request_review_comments" }
func (WebhookDelivery) TableName() string           { return "webhook_deliveries" }
func (RepositoryRefreshJob) TableName() string      { return "repository_refresh_jobs" }
func (GitRef) TableName() string                    { return "git_refs" }
func (GitCommit) TableName() string                 { return "git_commits" }
func (GitCommitParent) TableName() string           { return "git_commit_parents" }
func (GitCommitParentFile) TableName() string       { return "git_commit_parent_files" }
func (GitCommitParentHunk) TableName() string       { return "git_commit_parent_hunks" }
func (PullRequestChangeSnapshot) TableName() string { return "pull_request_change_snapshots" }
func (PullRequestChangeFile) TableName() string     { return "pull_request_change_files" }
func (PullRequestChangeHunk) TableName() string     { return "pull_request_change_hunks" }
func (RepoChangeSyncState) TableName() string       { return "repo_change_sync_states" }
func (RepoOpenPullInventory) TableName() string     { return "repo_open_pull_inventories" }
func (RepoTargetedPullRefresh) TableName() string   { return "repo_targeted_pull_refreshes" }
func (SearchDocument) TableName() string            { return "search_documents" }
func (RepoTextSearchState) TableName() string       { return "repo_text_search_states" }
