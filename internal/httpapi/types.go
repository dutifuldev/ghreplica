package httpapi

import (
	"encoding/json"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
)

type userResponse struct {
	Login     string `json:"login"`
	ID        int64  `json:"id"`
	NodeID    string `json:"node_id"`
	AvatarURL string `json:"avatar_url"`
	HTMLURL   string `json:"html_url"`
	Type      string `json:"type"`
	SiteAdmin bool   `json:"site_admin"`
	URL       string `json:"url"`
}

type repositoryResponse struct {
	ID            int64         `json:"id"`
	NodeID        string        `json:"node_id"`
	Name          string        `json:"name"`
	FullName      string        `json:"full_name"`
	Private       bool          `json:"private"`
	Owner         *userResponse `json:"owner"`
	HTMLURL       string        `json:"html_url"`
	Description   string        `json:"description"`
	Fork          bool          `json:"fork"`
	URL           string        `json:"url"`
	DefaultBranch string        `json:"default_branch"`
	Visibility    string        `json:"visibility"`
	Archived      bool          `json:"archived"`
	Disabled      bool          `json:"disabled"`
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
}

type issuePullRequestRef struct {
	URL      string `json:"url"`
	HTMLURL  string `json:"html_url"`
	DiffURL  string `json:"diff_url"`
	PatchURL string `json:"patch_url"`
}

type issueResponse struct {
	ID          int64                `json:"id"`
	NodeID      string               `json:"node_id"`
	Number      int                  `json:"number"`
	Title       string               `json:"title"`
	Body        string               `json:"body"`
	State       string               `json:"state"`
	StateReason string               `json:"state_reason,omitempty"`
	User        *userResponse        `json:"user"`
	Locked      bool                 `json:"locked"`
	Comments    int                  `json:"comments"`
	PullRequest *issuePullRequestRef `json:"pull_request,omitempty"`
	HTMLURL     string               `json:"html_url"`
	URL         string               `json:"url"`
	CreatedAt   time.Time            `json:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at"`
	ClosedAt    *time.Time           `json:"closed_at"`
}

type pullBranchRepositoryResponse struct {
	ID            int64         `json:"id"`
	NodeID        string        `json:"node_id"`
	Name          string        `json:"name"`
	FullName      string        `json:"full_name"`
	Private       bool          `json:"private"`
	Owner         *userResponse `json:"owner"`
	HTMLURL       string        `json:"html_url"`
	Description   string        `json:"description"`
	Fork          bool          `json:"fork"`
	URL           string        `json:"url"`
	DefaultBranch string        `json:"default_branch"`
	Visibility    string        `json:"visibility"`
	Archived      bool          `json:"archived"`
	Disabled      bool          `json:"disabled"`
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
}

type pullBranchResponse struct {
	Ref  string                        `json:"ref"`
	SHA  string                        `json:"sha"`
	Repo *pullBranchRepositoryResponse `json:"repo"`
}

type pullRequestResponse struct {
	ID             int64              `json:"id"`
	NodeID         string             `json:"node_id"`
	Number         int                `json:"number"`
	State          string             `json:"state"`
	Title          string             `json:"title"`
	Body           string             `json:"body"`
	User           *userResponse      `json:"user"`
	Draft          bool               `json:"draft"`
	Head           pullBranchResponse `json:"head"`
	Base           pullBranchResponse `json:"base"`
	Mergeable      *bool              `json:"mergeable"`
	MergeableState string             `json:"mergeable_state,omitempty"`
	Merged         bool               `json:"merged"`
	MergedAt       *time.Time         `json:"merged_at"`
	MergedBy       *userResponse      `json:"merged_by"`
	MergeCommitSHA string             `json:"merge_commit_sha,omitempty"`
	Additions      int                `json:"additions"`
	Deletions      int                `json:"deletions"`
	ChangedFiles   int                `json:"changed_files"`
	Commits        int                `json:"commits"`
	HTMLURL        string             `json:"html_url"`
	URL            string             `json:"url"`
	DiffURL        string             `json:"diff_url"`
	PatchURL       string             `json:"patch_url"`
	CreatedAt      time.Time          `json:"created_at"`
	UpdatedAt      time.Time          `json:"updated_at"`
}

type mirrorCountsResponse struct {
	Issues                    int64 `json:"issues"`
	Pulls                     int64 `json:"pulls"`
	IssueComments             int64 `json:"issue_comments"`
	PullRequestReviews        int64 `json:"pull_request_reviews"`
	PullRequestReviewComments int64 `json:"pull_request_review_comments"`
}

type mirrorStatusResponse struct {
	FullName                 string               `json:"full_name"`
	RepositoryPresent        bool                 `json:"repository_present"`
	TrackedRepositoryPresent bool                 `json:"tracked_repository_present"`
	TrackedRepositoryID      *uint                `json:"tracked_repository_id,omitempty"`
	RepositoryID             *uint                `json:"repository_id,omitempty"`
	Repository               *repositoryResponse  `json:"repository,omitempty"`
	Enabled                  bool                 `json:"enabled"`
	SyncMode                 string               `json:"sync_mode"`
	WebhookProjectionEnabled bool                 `json:"webhook_projection_enabled"`
	AllowManualBackfill      bool                 `json:"allow_manual_backfill"`
	IssuesCompleteness       string               `json:"issues_completeness"`
	PullsCompleteness        string               `json:"pulls_completeness"`
	CommentsCompleteness     string               `json:"comments_completeness"`
	ReviewsCompleteness      string               `json:"reviews_completeness"`
	LastBootstrapAt          *time.Time           `json:"last_bootstrap_at,omitempty"`
	LastCrawlAt              *time.Time           `json:"last_crawl_at,omitempty"`
	LastWebhookAt            *time.Time           `json:"last_webhook_at,omitempty"`
	Counts                   mirrorCountsResponse `json:"counts"`
}

type mirrorCompletenessResponse struct {
	Issues   string `json:"issues"`
	Pulls    string `json:"pulls"`
	Comments string `json:"comments"`
	Reviews  string `json:"reviews"`
}

type mirrorMetadataTimestampsResponse struct {
	LastWebhookAt   *time.Time `json:"last_webhook_at,omitempty"`
	LastBootstrapAt *time.Time `json:"last_bootstrap_at,omitempty"`
	LastCrawlAt     *time.Time `json:"last_crawl_at,omitempty"`
}

type mirrorRepositoryResponse struct {
	Owner        string                           `json:"owner"`
	Name         string                           `json:"name"`
	FullName     string                           `json:"full_name"`
	GitHubID     *int64                           `json:"github_id,omitempty"`
	NodeID       string                           `json:"node_id,omitempty"`
	Fork         *bool                            `json:"fork,omitempty"`
	Enabled      bool                             `json:"enabled"`
	SyncMode     string                           `json:"sync_mode"`
	Completeness mirrorCompletenessResponse       `json:"completeness"`
	Coverage     mirrorCountsResponse             `json:"coverage"`
	Timestamps   mirrorMetadataTimestampsResponse `json:"timestamps"`
}

type mirrorRepositoryRefResponse struct {
	Owner    string `json:"owner"`
	Name     string `json:"name"`
	FullName string `json:"full_name"`
}

type mirrorSyncResponse struct {
	State     string `json:"state"`
	LastError string `json:"last_error,omitempty"`
}

type mirrorPullRequestChangesResponse struct {
	Total   int `json:"total"`
	Current int `json:"current"`
	Stale   int `json:"stale"`
	Missing int `json:"missing"`
}

type mirrorActivityResponse struct {
	InventoryScanRunning      bool `json:"inventory_scan_running"`
	BackfillRunning           bool `json:"backfill_running"`
	TargetedRefreshPending    bool `json:"targeted_refresh_pending"`
	TargetedRefreshRunning    bool `json:"targeted_refresh_running"`
	InventoryRefreshRequested bool `json:"inventory_refresh_requested"`
}

type mirrorStatusTimestampsResponse struct {
	LastInventoryScanStartedAt  *time.Time `json:"last_inventory_scan_started_at,omitempty"`
	LastInventoryScanFinishedAt *time.Time `json:"last_inventory_scan_finished_at,omitempty"`
	LastBackfillStartedAt       *time.Time `json:"last_backfill_started_at,omitempty"`
	LastBackfillFinishedAt      *time.Time `json:"last_backfill_finished_at,omitempty"`
}

type mirrorRepositoryStatusResponse struct {
	Repository         mirrorRepositoryRefResponse      `json:"repository"`
	Sync               mirrorSyncResponse               `json:"sync"`
	PullRequestChanges mirrorPullRequestChangesResponse `json:"pull_request_changes"`
	Activity           mirrorActivityResponse           `json:"activity"`
	Timestamps         mirrorStatusTimestampsResponse   `json:"timestamps"`
}

func newRepositoryResponse(repo database.Repository) repositoryResponse {
	return repositoryResponse{
		ID:            repo.GitHubID,
		NodeID:        repo.NodeID,
		Name:          repo.Name,
		FullName:      repo.FullName,
		Private:       repo.Private,
		Owner:         newUserResponse(repo.Owner),
		HTMLURL:       repo.HTMLURL,
		Description:   repo.Description,
		Fork:          repo.Fork,
		URL:           repo.APIURL,
		DefaultBranch: repo.DefaultBranch,
		Visibility:    repo.Visibility,
		Archived:      repo.Archived,
		Disabled:      repo.Disabled,
		CreatedAt:     utcTime(repo.CreatedAt),
		UpdatedAt:     utcTime(repo.UpdatedAt),
	}
}

func newIssueResponse(issue database.Issue, prRef issuePullRequestRef) issueResponse {
	var pullRequest *issuePullRequestRef
	if issue.IsPullRequest {
		tmp := prRef
		pullRequest = &tmp
	}

	return issueResponse{
		ID:          issue.GitHubID,
		NodeID:      issue.NodeID,
		Number:      issue.Number,
		Title:       issue.Title,
		Body:        issue.Body,
		State:       issue.State,
		StateReason: issue.StateReason,
		User:        newUserResponse(issue.Author),
		Locked:      issue.Locked,
		Comments:    issue.CommentsCount,
		PullRequest: pullRequest,
		HTMLURL:     issue.HTMLURL,
		URL:         issue.APIURL,
		CreatedAt:   utcTime(issue.GitHubCreatedAt),
		UpdatedAt:   utcTime(issue.GitHubUpdatedAt),
		ClosedAt:    utcTimePtr(issue.ClosedAt),
	}
}

func newPullRequestResponse(pr database.PullRequest) pullRequestResponse {
	return pullRequestResponse{
		ID:             pr.GitHubID,
		NodeID:         pr.NodeID,
		Number:         pr.Number,
		State:          pr.State,
		Title:          pr.Issue.Title,
		Body:           pr.Issue.Body,
		User:           newUserResponse(pr.Issue.Author),
		Draft:          pr.Draft,
		Head:           newPullBranchResponse(pr.HeadRef, pr.HeadSHA, pr.HeadRepo),
		Base:           newPullBranchResponse(pr.BaseRef, pr.BaseSHA, pr.BaseRepo),
		Mergeable:      pr.Mergeable,
		MergeableState: pr.MergeableState,
		Merged:         pr.Merged,
		MergedAt:       utcTimePtr(pr.MergedAt),
		MergedBy:       newUserResponse(pr.MergedBy),
		MergeCommitSHA: pr.MergeCommitSHA,
		Additions:      pr.Additions,
		Deletions:      pr.Deletions,
		ChangedFiles:   pr.ChangedFiles,
		Commits:        pr.CommitsCount,
		HTMLURL:        pr.HTMLURL,
		URL:            pr.APIURL,
		DiffURL:        pr.DiffURL,
		PatchURL:       pr.PatchURL,
		CreatedAt:      utcTime(pr.GitHubCreatedAt),
		UpdatedAt:      utcTime(pr.GitHubUpdatedAt),
	}
}

func newPullBranchResponse(ref, sha string, repo *database.Repository) pullBranchResponse {
	return pullBranchResponse{
		Ref:  ref,
		SHA:  sha,
		Repo: newPullBranchRepositoryResponse(repo),
	}
}

func newPullBranchRepositoryResponse(repo *database.Repository) *pullBranchRepositoryResponse {
	if repo == nil {
		return nil
	}

	out := pullBranchRepositoryResponse{
		ID:            repo.GitHubID,
		NodeID:        repo.NodeID,
		Name:          repo.Name,
		FullName:      repo.FullName,
		Private:       repo.Private,
		Owner:         newUserResponse(repo.Owner),
		HTMLURL:       repo.HTMLURL,
		Description:   repo.Description,
		Fork:          repo.Fork,
		URL:           repo.APIURL,
		DefaultBranch: repo.DefaultBranch,
		Visibility:    repo.Visibility,
		Archived:      repo.Archived,
		Disabled:      repo.Disabled,
		CreatedAt:     utcTime(repo.CreatedAt),
		UpdatedAt:     utcTime(repo.UpdatedAt),
	}

	return &out
}

func newUserResponse(user *database.User) *userResponse {
	if user == nil {
		return nil
	}

	return &userResponse{
		Login:     user.Login,
		ID:        user.GitHubID,
		NodeID:    user.NodeID,
		AvatarURL: user.AvatarURL,
		HTMLURL:   user.HTMLURL,
		Type:      user.Type,
		SiteAdmin: user.SiteAdmin,
		URL:       user.APIURL,
	}
}

func decodeStoredJSON(raw []byte) (any, error) {
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func decodeStoredJSONArrayIssueComments(comments []database.IssueComment) ([]any, error) {
	out := make([]any, 0, len(comments))
	for _, comment := range comments {
		payload, err := decodeStoredJSON(comment.RawJSON)
		if err != nil {
			return nil, err
		}
		out = append(out, payload)
	}
	return out, nil
}

func decodeStoredJSONArrayIssues(issues []database.Issue) ([]any, error) {
	out := make([]any, 0, len(issues))
	for _, issue := range issues {
		payload, err := decodeStoredJSON(issue.RawJSON)
		if err != nil {
			return nil, err
		}
		out = append(out, payload)
	}
	return out, nil
}

func decodeStoredJSONArrayPullRequests(pulls []database.PullRequest) ([]any, error) {
	out := make([]any, 0, len(pulls))
	for _, pull := range pulls {
		payload, err := decodeStoredJSON(pull.RawJSON)
		if err != nil {
			return nil, err
		}
		out = append(out, payload)
	}
	return out, nil
}

func decodeStoredJSONArrayPullRequestReviews(reviews []database.PullRequestReview) ([]any, error) {
	out := make([]any, 0, len(reviews))
	for _, review := range reviews {
		payload, err := decodeStoredJSON(review.RawJSON)
		if err != nil {
			return nil, err
		}
		out = append(out, payload)
	}
	return out, nil
}

func decodeStoredJSONArrayPullRequestReviewComments(comments []database.PullRequestReviewComment) ([]any, error) {
	out := make([]any, 0, len(comments))
	for _, comment := range comments {
		payload, err := decodeStoredJSON(comment.RawJSON)
		if err != nil {
			return nil, err
		}
		out = append(out, payload)
	}
	return out, nil
}

func utcTime(value time.Time) time.Time {
	return value.UTC()
}

func utcTimePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	converted := value.UTC()
	return &converted
}

func uintPtr(value uint) *uint {
	if value == 0 {
		return nil
	}
	converted := value
	return &converted
}
