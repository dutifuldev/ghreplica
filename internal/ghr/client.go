package ghr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	gh "github.com/dutifuldev/ghreplica/internal/github"
)

type Client struct {
	baseURL string
	http    *http.Client
}

type MirrorCountsResponse struct {
	Issues                    int64 `json:"issues"`
	Pulls                     int64 `json:"pulls"`
	IssueComments             int64 `json:"issue_comments"`
	PullRequestReviews        int64 `json:"pull_request_reviews"`
	PullRequestReviewComments int64 `json:"pull_request_review_comments"`
}

type MirrorStatusResponse struct {
	FullName                 string                 `json:"full_name"`
	RepositoryPresent        bool                   `json:"repository_present"`
	TrackedRepositoryPresent bool                   `json:"tracked_repository_present"`
	TrackedRepositoryID      *uint                  `json:"tracked_repository_id"`
	RepositoryID             *uint                  `json:"repository_id"`
	Repository               *gh.RepositoryResponse `json:"repository"`
	Enabled                  bool                   `json:"enabled"`
	SyncMode                 string                 `json:"sync_mode"`
	WebhookProjectionEnabled bool                   `json:"webhook_projection_enabled"`
	AllowManualBackfill      bool                   `json:"allow_manual_backfill"`
	IssuesCompleteness       string                 `json:"issues_completeness"`
	PullsCompleteness        string                 `json:"pulls_completeness"`
	CommentsCompleteness     string                 `json:"comments_completeness"`
	ReviewsCompleteness      string                 `json:"reviews_completeness"`
	LastBootstrapAt          *time.Time             `json:"last_bootstrap_at"`
	LastCrawlAt              *time.Time             `json:"last_crawl_at"`
	LastWebhookAt            *time.Time             `json:"last_webhook_at"`
	Counts                   MirrorCountsResponse   `json:"counts"`
}

type MirrorCompletenessResponse struct {
	Issues   string `json:"issues"`
	Pulls    string `json:"pulls"`
	Comments string `json:"comments"`
	Reviews  string `json:"reviews"`
}

type MirrorMetadataTimestampsResponse struct {
	LastWebhookAt   *time.Time `json:"last_webhook_at"`
	LastBootstrapAt *time.Time `json:"last_bootstrap_at"`
	LastCrawlAt     *time.Time `json:"last_crawl_at"`
}

type MirrorRepositoryResponse struct {
	Owner        string                           `json:"owner"`
	Name         string                           `json:"name"`
	FullName     string                           `json:"full_name"`
	GitHubID     *int64                           `json:"github_id"`
	NodeID       string                           `json:"node_id"`
	Fork         *bool                            `json:"fork"`
	Enabled      bool                             `json:"enabled"`
	SyncMode     string                           `json:"sync_mode"`
	Completeness MirrorCompletenessResponse       `json:"completeness"`
	Timestamps   MirrorMetadataTimestampsResponse `json:"timestamps"`
}

type MirrorRepositoryRefResponse struct {
	Owner    string `json:"owner"`
	Name     string `json:"name"`
	FullName string `json:"full_name"`
}

type MirrorSyncResponse struct {
	State     string `json:"state"`
	LastError string `json:"last_error"`
}

type MirrorPullRequestChangesResponse struct {
	Total        int  `json:"total"`
	Current      int  `json:"current"`
	Stale        int  `json:"stale"`
	Missing      *int `json:"missing"`
	MissingStale bool `json:"missing_stale"`
}

type MirrorActivityResponse struct {
	InventoryScanRunning      bool `json:"inventory_scan_running"`
	BackfillRunning           bool `json:"backfill_running"`
	TargetedRefreshPending    bool `json:"targeted_refresh_pending"`
	TargetedRefreshRunning    bool `json:"targeted_refresh_running"`
	RecentPRRepairPending     bool `json:"recent_pr_repair_pending"`
	RecentPRRepairRunning     bool `json:"recent_pr_repair_running"`
	FullHistoryRepairRunning  bool `json:"full_history_repair_running"`
	InventoryRefreshRequested bool `json:"inventory_refresh_requested"`
}

type MirrorStatusTimestampsResponse struct {
	LastInventoryScanStartedAt      *time.Time `json:"last_inventory_scan_started_at"`
	LastInventoryScanFinishedAt     *time.Time `json:"last_inventory_scan_finished_at"`
	LastBackfillStartedAt           *time.Time `json:"last_backfill_started_at"`
	LastBackfillFinishedAt          *time.Time `json:"last_backfill_finished_at"`
	LastRecentPRRepairRequestedAt   *time.Time `json:"last_recent_pr_repair_requested_at"`
	LastRecentPRRepairStartedAt     *time.Time `json:"last_recent_pr_repair_started_at"`
	LastRecentPRRepairFinishedAt    *time.Time `json:"last_recent_pr_repair_finished_at"`
	LastFullHistoryRepairStartedAt  *time.Time `json:"last_full_history_repair_started_at"`
	LastFullHistoryRepairFinishedAt *time.Time `json:"last_full_history_repair_finished_at"`
}

type MirrorRepositoryStatusResponse struct {
	Repository         MirrorRepositoryRefResponse      `json:"repository"`
	Sync               MirrorSyncResponse               `json:"sync"`
	PullRequestChanges MirrorPullRequestChangesResponse `json:"pull_request_changes"`
	Activity           MirrorActivityResponse           `json:"activity"`
	Timestamps         MirrorStatusTimestampsResponse   `json:"timestamps"`
}

func NewClient(baseURL string) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = "https://ghreplica.dutiful.dev"
	}
	return &Client{
		baseURL: baseURL,
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) GetRepository(ctx context.Context, repo string) (gh.RepositoryResponse, error) {
	var out gh.RepositoryResponse
	err := c.getJSON(ctx, "/v1/github/repos/"+repo, &out)
	return out, err
}

func (c *Client) GetMirrorStatus(ctx context.Context, repo string) (MirrorStatusResponse, error) {
	var out MirrorStatusResponse
	err := c.getJSON(ctx, "/v1/changes/repos/"+repo+"/mirror-status", &out)
	return out, err
}

func (c *Client) ListMirrorRepositories(ctx context.Context, page, perPage int) ([]MirrorRepositoryResponse, error) {
	path := "/v1/mirror/repos"
	q := url.Values{}
	if page > 0 {
		q.Set("page", fmt.Sprintf("%d", page))
	}
	if perPage > 0 {
		q.Set("per_page", fmt.Sprintf("%d", perPage))
	}
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}

	var out []MirrorRepositoryResponse
	err := c.getJSON(ctx, path, &out)
	return out, err
}

func (c *Client) GetMirrorRepository(ctx context.Context, repo string) (MirrorRepositoryResponse, error) {
	var out MirrorRepositoryResponse
	err := c.getJSON(ctx, "/v1/mirror/repos/"+repo, &out)
	return out, err
}

func (c *Client) GetMirrorRepositoryStatus(ctx context.Context, repo string) (MirrorRepositoryStatusResponse, error) {
	var out MirrorRepositoryStatusResponse
	err := c.getJSON(ctx, "/v1/mirror/repos/"+repo+"/status", &out)
	return out, err
}

func (c *Client) ListIssues(ctx context.Context, repo, state string, limit int) ([]gh.IssueResponse, error) {
	path := "/v1/github/repos/" + repo + "/issues"
	if state != "" || limit > 0 {
		q := url.Values{}
		if state != "" {
			q.Set("state", state)
		}
		if limit > 0 {
			q.Set("per_page", fmt.Sprintf("%d", limit))
		}
		path += "?" + q.Encode()
	}
	var out []gh.IssueResponse
	err := c.getJSON(ctx, path, &out)
	return out, err
}

func (c *Client) GetIssue(ctx context.Context, repo string, number int) (gh.IssueResponse, error) {
	var out gh.IssueResponse
	err := c.getJSON(ctx, fmt.Sprintf("/v1/github/repos/%s/issues/%d", repo, number), &out)
	return out, err
}

func (c *Client) ListIssueComments(ctx context.Context, repo string, number int) ([]gh.IssueCommentResponse, error) {
	var out []gh.IssueCommentResponse
	err := c.getJSON(ctx, fmt.Sprintf("/v1/github/repos/%s/issues/%d/comments", repo, number), &out)
	return out, err
}

func (c *Client) ListPullRequests(ctx context.Context, repo, state string, limit int) ([]gh.PullRequestResponse, error) {
	path := "/v1/github/repos/" + repo + "/pulls"
	if state != "" || limit > 0 {
		q := url.Values{}
		if state != "" {
			q.Set("state", state)
		}
		if limit > 0 {
			q.Set("per_page", fmt.Sprintf("%d", limit))
		}
		path += "?" + q.Encode()
	}
	var out []gh.PullRequestResponse
	err := c.getJSON(ctx, path, &out)
	return out, err
}

func (c *Client) GetPullRequest(ctx context.Context, repo string, number int) (gh.PullRequestResponse, error) {
	var out gh.PullRequestResponse
	err := c.getJSON(ctx, fmt.Sprintf("/v1/github/repos/%s/pulls/%d", repo, number), &out)
	return out, err
}

func (c *Client) ListPullRequestReviews(ctx context.Context, repo string, number int) ([]gh.PullRequestReviewResponse, error) {
	var out []gh.PullRequestReviewResponse
	err := c.getJSON(ctx, fmt.Sprintf("/v1/github/repos/%s/pulls/%d/reviews", repo, number), &out)
	return out, err
}

func (c *Client) ListPullRequestComments(ctx context.Context, repo string, number int) ([]gh.PullRequestReviewCommentResponse, error) {
	var out []gh.PullRequestReviewCommentResponse
	err := c.getJSON(ctx, fmt.Sprintf("/v1/github/repos/%s/pulls/%d/comments", repo, number), &out)
	return out, err
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	target := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer closeGHRBody(resp.Body)

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = resp.Status
		}
		return fmt.Errorf("GET %s: %s", target, msg)
	}

	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) postJSON(ctx context.Context, path string, requestBody any, out any) error {
	target := c.baseURL + path
	payload, err := json.Marshal(requestBody)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer closeGHRBody(resp.Body)

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = resp.Status
		}
		return fmt.Errorf("POST %s: %s", target, msg)
	}

	return json.NewDecoder(resp.Body).Decode(out)
}

func closeGHRBody(closer io.Closer) {
	if closer != nil {
		_ = closer.Close()
	}
}
