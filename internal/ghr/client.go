package ghr

import (
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
	err := c.getJSON(ctx, "/repos/"+repo, &out)
	return out, err
}

func (c *Client) ListIssues(ctx context.Context, repo, state string, limit int) ([]gh.IssueResponse, error) {
	path := "/repos/" + repo + "/issues"
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
	err := c.getJSON(ctx, fmt.Sprintf("/repos/%s/issues/%d", repo, number), &out)
	return out, err
}

func (c *Client) ListIssueComments(ctx context.Context, repo string, number int) ([]gh.IssueCommentResponse, error) {
	var out []gh.IssueCommentResponse
	err := c.getJSON(ctx, fmt.Sprintf("/repos/%s/issues/%d/comments", repo, number), &out)
	return out, err
}

func (c *Client) ListPullRequests(ctx context.Context, repo, state string, limit int) ([]gh.PullRequestResponse, error) {
	path := "/repos/" + repo + "/pulls"
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
	err := c.getJSON(ctx, fmt.Sprintf("/repos/%s/pulls/%d", repo, number), &out)
	return out, err
}

func (c *Client) ListPullRequestReviews(ctx context.Context, repo string, number int) ([]gh.PullRequestReviewResponse, error) {
	var out []gh.PullRequestReviewResponse
	err := c.getJSON(ctx, fmt.Sprintf("/repos/%s/pulls/%d/reviews", repo, number), &out)
	return out, err
}

func (c *Client) ListPullRequestComments(ctx context.Context, repo string, number int) ([]gh.PullRequestReviewCommentResponse, error) {
	var out []gh.PullRequestReviewCommentResponse
	err := c.getJSON(ctx, fmt.Sprintf("/repos/%s/pulls/%d/comments", repo, number), &out)
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
