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
	err := c.getJSON(ctx, "/repos/"+repo+"/_ghreplica", &out)
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
	defer resp.Body.Close()

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
	defer resp.Body.Close()

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
