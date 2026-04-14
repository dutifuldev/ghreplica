package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   strings.TrimSpace(token),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) GetRepository(ctx context.Context, owner, repo string) (RepositoryResponse, error) {
	var out RepositoryResponse
	err := c.getJSON(ctx, fmt.Sprintf("/repos/%s/%s", owner, repo), &out)
	return out, err
}

func (c *Client) ListIssues(ctx context.Context, owner, repo, state string) ([]IssueResponse, error) {
	path := fmt.Sprintf("/repos/%s/%s/issues?state=%s&per_page=100", owner, repo, url.QueryEscape(state))
	return listAll[IssueResponse](ctx, c, path)
}

func (c *Client) ListPullRequests(ctx context.Context, owner, repo, state string) ([]PullRequestResponse, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls?state=%s&per_page=100", owner, repo, url.QueryEscape(state))
	return listAll[PullRequestResponse](ctx, c, path)
}

func listAll[T any](ctx context.Context, client *Client, path string) ([]T, error) {
	results := make([]T, 0)
	nextPath := path

	for nextPath != "" {
		req, err := client.newRequest(ctx, nextPath)
		if err != nil {
			return nil, err
		}

		resp, err := client.httpClient.Do(req)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode >= 300 {
			defer resp.Body.Close()
			return nil, decodeHTTPError(resp)
		}

		var page []T
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			resp.Body.Close()
			return nil, err
		}
		resp.Body.Close()

		results = append(results, page...)
		nextPath = nextLink(resp.Header.Get("Link"))
	}

	return results, nil
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	req, err := c.newRequest(ctx, path)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return decodeHTTPError(resp)
	}

	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) newRequest(ctx context.Context, path string) (*http.Request, error) {
	target := path
	if !strings.HasPrefix(path, "http://") && !strings.HasPrefix(path, "https://") {
		target = c.baseURL + path
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "ghreplica")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	return req, nil
}

func nextLink(header string) string {
	if strings.TrimSpace(header) == "" {
		return ""
	}

	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if strings.HasSuffix(part, `rel="next"`) {
			start := strings.Index(part, "<")
			end := strings.Index(part, ">")
			if start >= 0 && end > start {
				return part[start+1 : end]
			}
		}
	}

	return ""
}

func decodeHTTPError(resp *http.Response) error {
	return fmt.Errorf("github api returned %s", resp.Status)
}
