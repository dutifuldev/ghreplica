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
	Total        int  `json:"total"`
	Current      int  `json:"current"`
	Stale        int  `json:"stale"`
	Missing      *int `json:"missing"`
	MissingStale bool `json:"missing_stale"`
}

type mirrorActivityResponse struct {
	InventoryScanRunning      bool `json:"inventory_scan_running"`
	BackfillRunning           bool `json:"backfill_running"`
	TargetedRefreshPending    bool `json:"targeted_refresh_pending"`
	TargetedRefreshRunning    bool `json:"targeted_refresh_running"`
	RecentPRRepairPending     bool `json:"recent_pr_repair_pending"`
	RecentPRRepairRunning     bool `json:"recent_pr_repair_running"`
	FullHistoryRepairRunning  bool `json:"full_history_repair_running"`
	InventoryRefreshRequested bool `json:"inventory_refresh_requested"`
}

type mirrorStatusTimestampsResponse struct {
	LastInventoryScanStartedAt      *time.Time `json:"last_inventory_scan_started_at,omitempty"`
	LastInventoryScanFinishedAt     *time.Time `json:"last_inventory_scan_finished_at,omitempty"`
	LastBackfillStartedAt           *time.Time `json:"last_backfill_started_at,omitempty"`
	LastBackfillFinishedAt          *time.Time `json:"last_backfill_finished_at,omitempty"`
	LastRecentPRRepairRequestedAt   *time.Time `json:"last_recent_pr_repair_requested_at,omitempty"`
	LastRecentPRRepairStartedAt     *time.Time `json:"last_recent_pr_repair_started_at,omitempty"`
	LastRecentPRRepairFinishedAt    *time.Time `json:"last_recent_pr_repair_finished_at,omitempty"`
	LastFullHistoryRepairStartedAt  *time.Time `json:"last_full_history_repair_started_at,omitempty"`
	LastFullHistoryRepairFinishedAt *time.Time `json:"last_full_history_repair_finished_at,omitempty"`
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
