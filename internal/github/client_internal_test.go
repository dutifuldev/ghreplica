package github

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestClientResourceEndpointsAndPagination(t *testing.T) {
	now := time.Now().UTC()
	server := newClientResourceServer(t, now)
	defer server.Close()

	client := NewClient(server.URL, AuthConfig{Token: "static-token"})

	repo, err := client.GetRepository(context.Background(), "acme", "widgets")
	require.NoError(t, err)
	require.Equal(t, "acme/widgets", repo.FullName)

	issues, err := client.ListIssues(context.Background(), "acme", "widgets", "open")
	require.NoError(t, err)
	require.Len(t, issues, 2)

	issues, err = client.ListIssuesPage(context.Background(), "acme", "widgets", "", "", "", 0, 0)
	require.NoError(t, err)
	require.Len(t, issues, 1)

	pulls, err := client.ListPullRequests(context.Background(), "acme", "widgets", "open")
	require.NoError(t, err)
	require.Len(t, pulls, 1)

	pulls, err = client.ListPullRequestsPage(context.Background(), "acme", "widgets", "", "", "", 0, 0)
	require.NoError(t, err)
	require.Len(t, pulls, 1)

	issue, err := client.GetIssue(context.Background(), "acme", "widgets", 7)
	require.NoError(t, err)
	require.Equal(t, 7, issue.Number)

	pull, err := client.GetPullRequest(context.Background(), "acme", "widgets", 8)
	require.NoError(t, err)
	require.Equal(t, 8, pull.Number)

	issueComments, err := client.ListIssueComments(context.Background(), "acme", "widgets")
	require.NoError(t, err)
	require.Len(t, issueComments, 1)

	issueComments, err = client.ListIssueCommentsForIssue(context.Background(), "acme", "widgets", 7)
	require.NoError(t, err)
	require.Len(t, issueComments, 1)

	reviews, err := client.ListPullRequestReviews(context.Background(), "acme", "widgets", 8)
	require.NoError(t, err)
	require.Len(t, reviews, 1)

	reviewComments, err := client.ListPullRequestReviewComments(context.Background(), "acme", "widgets", 8)
	require.NoError(t, err)
	require.Len(t, reviewComments, 1)
}

func newClientResourceServer(t *testing.T, now time.Time) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer static-token", r.Header.Get("Authorization"))
		if handleClientRepositoryRoute(w, r, now) || handleClientIssueRoutes(t, w, r, now) || handleClientPullRoutes(t, w, r, now) {
			return
		}
		http.NotFound(w, r)
	}))
}

func handleClientRepositoryRoute(w http.ResponseWriter, r *http.Request, now time.Time) bool {
	if r.URL.Path != "/repos/acme/widgets" {
		return false
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":         1,
		"node_id":    "repo1",
		"name":       "widgets",
		"full_name":  "acme/widgets",
		"private":    false,
		"html_url":   "https://github.com/acme/widgets",
		"url":        "https://api.github.test/repos/acme/widgets",
		"created_at": now,
		"updated_at": now,
	})
	return true
}

func handleClientIssueRoutes(t *testing.T, w http.ResponseWriter, r *http.Request, now time.Time) bool {
	if handleClientIssueListRoute(t, w, r, now) {
		return true
	}
	switch r.URL.Path {
	case "/repos/acme/widgets/issues/7":
		_ = json.NewEncoder(w).Encode(issueJSON(7, "issue 7", now))
	case "/repos/acme/widgets/issues/comments":
		_ = json.NewEncoder(w).Encode([]map[string]any{issueCommentJSON(21, now)})
	case "/repos/acme/widgets/issues/7/comments":
		_ = json.NewEncoder(w).Encode([]map[string]any{issueCommentJSON(22, now)})
	default:
		return false
	}
	return true
}

func handleClientIssueListRoute(t *testing.T, w http.ResponseWriter, r *http.Request, now time.Time) bool {
	if r.URL.Path != "/repos/acme/widgets/issues" {
		return false
	}
	switch {
	case r.URL.Query().Get("sort") == "updated":
		require.Equal(t, "open", r.URL.Query().Get("state"))
		require.Equal(t, "desc", r.URL.Query().Get("direction"))
		require.Equal(t, "1", r.URL.Query().Get("page"))
		require.Equal(t, "100", r.URL.Query().Get("per_page"))
		_ = json.NewEncoder(w).Encode([]map[string]any{issueJSON(3, "paged issue", now)})
	case r.URL.Query().Get("page") == "":
		require.Equal(t, "open", r.URL.Query().Get("state"))
		require.Equal(t, "100", r.URL.Query().Get("per_page"))
		w.Header().Set("Link", serverLinkHeader(serverURL(r), "/repos/acme/widgets/issues?page=2&state=open&per_page=100"))
		_ = json.NewEncoder(w).Encode([]map[string]any{issueJSON(1, "first", now)})
	case r.URL.Query().Get("page") == "2":
		_ = json.NewEncoder(w).Encode([]map[string]any{issueJSON(2, "second", now)})
	default:
		return false
	}
	return true
}

func handleClientPullRoutes(t *testing.T, w http.ResponseWriter, r *http.Request, now time.Time) bool {
	switch {
	case r.URL.Path == "/repos/acme/widgets/pulls" && r.URL.Query().Get("page") == "":
		require.Equal(t, "open", r.URL.Query().Get("state"))
		require.Equal(t, "100", r.URL.Query().Get("per_page"))
		_ = json.NewEncoder(w).Encode([]map[string]any{pullJSON(10, "open pull", now)})
	case r.URL.Path == "/repos/acme/widgets/pulls" && r.URL.Query().Get("sort") == "updated":
		require.Equal(t, "open", r.URL.Query().Get("state"))
		require.Equal(t, "desc", r.URL.Query().Get("direction"))
		_ = json.NewEncoder(w).Encode([]map[string]any{pullJSON(11, "paged pull", now)})
	case r.URL.Path == "/repos/acme/widgets/pulls/8":
		_ = json.NewEncoder(w).Encode(pullJSON(8, "pull 8", now))
	case r.URL.Path == "/repos/acme/widgets/pulls/8/reviews":
		_ = json.NewEncoder(w).Encode([]map[string]any{pullReviewJSON(31, now)})
	case r.URL.Path == "/repos/acme/widgets/pulls/8/comments":
		_ = json.NewEncoder(w).Encode([]map[string]any{pullReviewCommentJSON(41, now)})
	default:
		return false
	}
	return true
}

func TestClientErrorHelpersAndAuthFallbacks(t *testing.T) {
	err := decodeHTTPError(&http.Response{
		StatusCode: http.StatusTooManyRequests,
		Status:     "429 Too Many Requests",
		Body:       io.NopCloser(strings.NewReader("rate limited")),
	})
	var httpErr *HTTPError
	require.ErrorAs(t, err, &httpErr)
	require.Equal(t, "github api returned 429: rate limited", httpErr.Error())
	require.True(t, httpErr.Temporary())

	err = decodeHTTPError(&http.Response{
		StatusCode: http.StatusNotFound,
		Status:     "404 Not Found",
		Body:       io.NopCloser(strings.NewReader("")),
	})
	require.ErrorAs(t, err, &httpErr)
	require.False(t, httpErr.Temporary())
	require.Equal(t, "github api returned 404: github api returned 404 Not Found", httpErr.Error())

	require.Equal(t, "https://api.github.test/next?page=2", nextLink(`<https://api.github.test/prev?page=1>; rel="prev", <https://api.github.test/next?page=2>; rel="next"`))
	require.Equal(t, "", nextLink(""))

	client := NewClient("https://api.github.test", AuthConfig{})
	token, err := client.AuthorizationToken(context.Background())
	require.NoError(t, err)
	require.Empty(t, token)

	_, err = NewClient("https://api.github.test", AuthConfig{AppID: "1", InstallationID: "2", PrivateKeyPEM: "not a pem"}).appJWT()
	require.Error(t, err)

	_, err = NewClient("https://api.github.test", AuthConfig{AppID: "1", InstallationID: "2", PrivateKeyPath: filepath.Join(t.TempDir(), "missing.pem")}).privateKey()
	require.Error(t, err)

	pkcs1 := generatePrivateKeyPEM(t, false)
	path := filepath.Join(t.TempDir(), "key.pem")
	require.NoError(t, os.WriteFile(path, pkcs1, 0o644))
	key, err := NewClient("https://api.github.test", AuthConfig{PrivateKeyPath: path}).privateKey()
	require.NoError(t, err)
	require.NotNil(t, key)

	pkcs8 := generatePrivateKeyPEM(t, true)
	key, err = NewClient("https://api.github.test", AuthConfig{PrivateKeyPEM: string(pkcs8)}).privateKey()
	require.NoError(t, err)
	require.NotNil(t, key)
}

func generatePrivateKeyPEM(t *testing.T, pkcs8 bool) []byte {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	if pkcs8 {
		body, err := x509.MarshalPKCS8PrivateKey(privateKey)
		require.NoError(t, err)
		return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: body})
	}
	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})
}

func serverLinkHeader(baseURL, nextPath string) string {
	return "<" + baseURL + nextPath + `>; rel="next"`
}

func serverURL(r *http.Request) string {
	scheme := "http://"
	if r.TLS != nil {
		scheme = "https://"
	}
	return scheme + r.Host
}

func issueJSON(number int, title string, now time.Time) map[string]any {
	return map[string]any{
		"id":         number + 1000,
		"node_id":    "issue",
		"number":     number,
		"title":      title,
		"state":      "open",
		"html_url":   "https://github.com/acme/widgets/issues/1",
		"url":        "https://api.github.test/repos/acme/widgets/issues/1",
		"created_at": now,
		"updated_at": now,
		"user": map[string]any{
			"id":         10,
			"login":      "octocat",
			"avatar_url": "https://avatars.githubusercontent.com/u/10?v=4",
			"html_url":   "https://github.com/octocat",
			"url":        "https://api.github.test/users/octocat",
			"type":       "User",
			"site_admin": false,
		},
	}
}

func pullJSON(number int, title string, now time.Time) map[string]any {
	return map[string]any{
		"id":         number + 2000,
		"node_id":    "pull",
		"number":     number,
		"title":      title,
		"state":      "open",
		"html_url":   "https://github.com/acme/widgets/pull/1",
		"url":        "https://api.github.test/repos/acme/widgets/pulls/1",
		"issue_url":  "https://api.github.test/repos/acme/widgets/issues/1",
		"diff_url":   "https://github.com/acme/widgets/pull/1.diff",
		"patch_url":  "https://github.com/acme/widgets/pull/1.patch",
		"created_at": now,
		"updated_at": now,
		"user": map[string]any{
			"id":         11,
			"login":      "reviewer",
			"avatar_url": "https://avatars.githubusercontent.com/u/11?v=4",
			"html_url":   "https://github.com/reviewer",
			"url":        "https://api.github.test/users/reviewer",
			"type":       "User",
			"site_admin": false,
		},
		"head": map[string]any{"ref": "feature", "sha": "abc", "label": "acme:feature", "repo": nil},
		"base": map[string]any{"ref": "main", "sha": "def", "label": "acme:main", "repo": nil},
	}
}

func issueCommentJSON(id int, now time.Time) map[string]any {
	return map[string]any{
		"id":         id,
		"node_id":    "comment",
		"body":       "hello",
		"html_url":   "https://github.com/acme/widgets/issues/1#issuecomment-1",
		"url":        "https://api.github.test/repos/acme/widgets/issues/comments/1",
		"created_at": now,
		"updated_at": now,
		"user": map[string]any{
			"id":         12,
			"login":      "commenter",
			"avatar_url": "https://avatars.githubusercontent.com/u/12?v=4",
			"html_url":   "https://github.com/commenter",
			"url":        "https://api.github.test/users/commenter",
			"type":       "User",
			"site_admin": false,
		},
	}
}

func pullReviewJSON(id int, now time.Time) map[string]any {
	return map[string]any{
		"id":           id,
		"node_id":      "review",
		"body":         "looks good",
		"state":        "APPROVED",
		"html_url":     "https://github.com/acme/widgets/pull/1#pullrequestreview-1",
		"url":          "https://api.github.test/repos/acme/widgets/pulls/1/reviews/1",
		"submitted_at": now,
		"created_at":   now,
		"updated_at":   now,
		"user": map[string]any{
			"id":         13,
			"login":      "approver",
			"avatar_url": "https://avatars.githubusercontent.com/u/13?v=4",
			"html_url":   "https://github.com/approver",
			"url":        "https://api.github.test/users/approver",
			"type":       "User",
			"site_admin": false,
		},
	}
}

func pullReviewCommentJSON(id int, now time.Time) map[string]any {
	return map[string]any{
		"id":               id,
		"node_id":          "review_comment",
		"body":             "nit",
		"path":             "main.go",
		"diff_hunk":        "@@",
		"html_url":         "https://github.com/acme/widgets/pull/1#discussion_r1",
		"url":              "https://api.github.test/repos/acme/widgets/pulls/comments/1",
		"pull_request_url": "https://api.github.test/repos/acme/widgets/pulls/1",
		"created_at":       now,
		"updated_at":       now,
		"user": map[string]any{
			"id":         14,
			"login":      "review-bot",
			"avatar_url": "https://avatars.githubusercontent.com/u/14?v=4",
			"html_url":   "https://github.com/review-bot",
			"url":        "https://api.github.test/users/review-bot",
			"type":       "User",
			"site_admin": false,
		},
	}
}
