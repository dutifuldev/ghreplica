package github

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Client struct {
	baseURL     string
	auth        AuthConfig
	httpClient  *http.Client
	mu          sync.Mutex
	cachedToken string
	tokenExpiry time.Time
}

type AuthConfig struct {
	Token          string
	AppID          string
	InstallationID string
	PrivateKeyPEM  string
	PrivateKeyPath string
}

func NewClient(baseURL string, auth AuthConfig) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		auth: AuthConfig{
			Token:          strings.TrimSpace(auth.Token),
			AppID:          strings.TrimSpace(auth.AppID),
			InstallationID: strings.TrimSpace(auth.InstallationID),
			PrivateKeyPEM:  strings.TrimSpace(auth.PrivateKeyPEM),
			PrivateKeyPath: strings.TrimSpace(auth.PrivateKeyPath),
		},
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

func (c *Client) ListIssuesPage(ctx context.Context, owner, repo, state, sortField, direction string, page, perPage int) ([]IssueResponse, error) {
	if strings.TrimSpace(state) == "" {
		state = "open"
	}
	if strings.TrimSpace(sortField) == "" {
		sortField = "updated"
	}
	if strings.TrimSpace(direction) == "" {
		direction = "desc"
	}
	if page <= 0 {
		page = 1
	}
	if perPage <= 0 {
		perPage = 100
	}

	path := fmt.Sprintf(
		"/repos/%s/%s/issues?state=%s&sort=%s&direction=%s&page=%d&per_page=%d",
		owner,
		repo,
		url.QueryEscape(state),
		url.QueryEscape(sortField),
		url.QueryEscape(direction),
		page,
		perPage,
	)
	var out []IssueResponse
	if err := c.getJSON(ctx, path, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) ListPullRequests(ctx context.Context, owner, repo, state string) ([]PullRequestResponse, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls?state=%s&per_page=100", owner, repo, url.QueryEscape(state))
	return listAll[PullRequestResponse](ctx, c, path)
}

func (c *Client) ListPullRequestsPage(ctx context.Context, owner, repo, state, sortField, direction string, page, perPage int) ([]PullRequestResponse, error) {
	if strings.TrimSpace(state) == "" {
		state = "open"
	}
	if strings.TrimSpace(sortField) == "" {
		sortField = "updated"
	}
	if strings.TrimSpace(direction) == "" {
		direction = "desc"
	}
	if page <= 0 {
		page = 1
	}
	if perPage <= 0 {
		perPage = 100
	}

	path := fmt.Sprintf(
		"/repos/%s/%s/pulls?state=%s&sort=%s&direction=%s&page=%d&per_page=%d",
		owner,
		repo,
		url.QueryEscape(state),
		url.QueryEscape(sortField),
		url.QueryEscape(direction),
		page,
		perPage,
	)
	var out []PullRequestResponse
	if err := c.getJSON(ctx, path, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) GetIssue(ctx context.Context, owner, repo string, number int) (IssueResponse, error) {
	var out IssueResponse
	err := c.getJSON(ctx, fmt.Sprintf("/repos/%s/%s/issues/%d", owner, repo, number), &out)
	return out, err
}

func (c *Client) GetPullRequest(ctx context.Context, owner, repo string, number int) (PullRequestResponse, error) {
	var out PullRequestResponse
	err := c.getJSON(ctx, fmt.Sprintf("/repos/%s/%s/pulls/%d", owner, repo, number), &out)
	return out, err
}

func (c *Client) ListIssueComments(ctx context.Context, owner, repo string) ([]IssueCommentResponse, error) {
	return listAll[IssueCommentResponse](ctx, c, fmt.Sprintf("/repos/%s/%s/issues/comments?per_page=100", owner, repo))
}

func (c *Client) ListIssueCommentsForIssue(ctx context.Context, owner, repo string, number int) ([]IssueCommentResponse, error) {
	return listAll[IssueCommentResponse](ctx, c, fmt.Sprintf("/repos/%s/%s/issues/%d/comments?per_page=100", owner, repo, number))
}

func (c *Client) ListPullRequestReviews(ctx context.Context, owner, repo string, number int) ([]PullRequestReviewResponse, error) {
	return listAll[PullRequestReviewResponse](ctx, c, fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews?per_page=100", owner, repo, number))
}

func (c *Client) ListPullRequestReviewComments(ctx context.Context, owner, repo string, number int) ([]PullRequestReviewCommentResponse, error) {
	return listAll[PullRequestReviewCommentResponse](ctx, c, fmt.Sprintf("/repos/%s/%s/pulls/%d/comments?per_page=100", owner, repo, number))
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

func (c *Client) AuthorizationToken(ctx context.Context) (string, error) {
	return c.authorizationToken(ctx)
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
	token, err := c.authorizationToken(ctx)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	return req, nil
}

func (c *Client) authorizationToken(ctx context.Context) (string, error) {
	if c.auth.Token != "" {
		return c.auth.Token, nil
	}
	if c.auth.AppID == "" || c.auth.InstallationID == "" {
		return "", nil
	}

	c.mu.Lock()
	if c.cachedToken != "" && time.Until(c.tokenExpiry) > 2*time.Minute {
		token := c.cachedToken
		c.mu.Unlock()
		return token, nil
	}
	c.mu.Unlock()

	token, expiry, err := c.fetchInstallationToken(ctx)
	if err != nil {
		return "", err
	}

	c.mu.Lock()
	c.cachedToken = token
	c.tokenExpiry = expiry
	c.mu.Unlock()
	return token, nil
}

func (c *Client) fetchInstallationToken(ctx context.Context) (string, time.Time, error) {
	jwtToken, err := c.appJWT()
	if err != nil {
		return "", time.Time{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+fmt.Sprintf("/app/installations/%s/access_tokens", c.auth.InstallationID), bytes.NewReader([]byte("{}")))
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "ghreplica")
	req.Header.Set("Authorization", "Bearer "+jwtToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", time.Time{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", time.Time{}, decodeHTTPError(resp)
	}

	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", time.Time{}, err
	}
	return out.Token, out.ExpiresAt, nil
}

func (c *Client) appJWT() (string, error) {
	key, err := c.privateKey()
	if err != nil {
		return "", err
	}

	now := time.Now().UTC()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payloadBytes, err := json.Marshal(map[string]any{
		"iat": now.Add(-time.Minute).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": c.auth.AppID,
	})
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	signingInput := header + "." + payload

	hashed := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hashed[:])
	if err != nil {
		return "", err
	}

	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (c *Client) privateKey() (*rsa.PrivateKey, error) {
	raw := c.auth.PrivateKeyPEM
	if raw == "" && c.auth.PrivateKeyPath != "" {
		body, err := os.ReadFile(c.auth.PrivateKeyPath)
		if err != nil {
			return nil, err
		}
		raw = string(body)
	}
	block, _ := pem.Decode([]byte(raw))
	if block == nil {
		return nil, fmt.Errorf("github app private key is invalid")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rsaKey, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("github app private key must be RSA")
	}
	return rsaKey, nil
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

type HTTPError struct {
	StatusCode int
	Message    string
}

func (e *HTTPError) Error() string {
	return e.Message
}

func (e *HTTPError) Temporary() bool {
	return e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= 500
}

func decodeHTTPError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	message := strings.TrimSpace(string(body))
	if message == "" {
		message = fmt.Sprintf("github api returned %s", resp.Status)
	}

	return &HTTPError{
		StatusCode: resp.StatusCode,
		Message:    fmt.Sprintf("github api returned %s: %s", strconv.Itoa(resp.StatusCode), message),
	}
}
