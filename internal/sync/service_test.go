package sync_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/dutifuldev/ghreplica/internal/github"
	"github.com/dutifuldev/ghreplica/internal/httpapi"
	syncsvc "github.com/dutifuldev/ghreplica/internal/sync"
	"github.com/stretchr/testify/require"
)

func TestBootstrapRepositoryAndServeGitHubLikeEndpoints(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open("sqlite://file::memory:?cache=shared")
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	githubServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/widgets":
			writeJSON(t, w, repoFixture())
		case "/repos/acme/widgets/issues":
			writeJSON(t, w, issuesFixture())
		case "/repos/acme/widgets/pulls":
			writeJSON(t, w, pullsFixture())
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(githubServer.Close)

	service := syncsvc.NewService(db, github.NewClient(githubServer.URL, ""))
	require.NoError(t, service.BootstrapRepository(ctx, "acme", "widgets"))

	server := httpapi.NewServer(db)

	req := httptest.NewRequest(http.MethodGet, "/repos/acme/widgets", nil)
	rec := httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var repo map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &repo))
	require.Equal(t, "acme/widgets", repo["full_name"])

	req = httptest.NewRequest(http.MethodGet, "/repos/acme/widgets/issues?state=all", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var issues []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &issues))
	require.Len(t, issues, 2)
	require.Equal(t, "Fix parser", issues[0]["title"])
	require.NotNil(t, issues[0]["pull_request"])

	req = httptest.NewRequest(http.MethodGet, "/repos/acme/widgets/pulls?state=all", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var pulls []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &pulls))
	require.Len(t, pulls, 1)
	require.Equal(t, "Fix parser", pulls[0]["title"])
	require.Equal(t, "fix/parser", pulls[0]["head"].(map[string]any)["ref"])
}

func repoFixture() github.RepositoryResponse {
	now := time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC)
	return github.RepositoryResponse{
		ID:            101,
		NodeID:        "R_kgDORepo",
		Name:          "widgets",
		FullName:      "acme/widgets",
		Private:       false,
		Owner:         &github.UserResponse{ID: 11, NodeID: "U_owner", Login: "acme", Type: "Organization", AvatarURL: "https://example.com/acme.png", HTMLURL: "https://github.com/acme", URL: "https://api.github.com/users/acme"},
		HTMLURL:       "https://github.com/acme/widgets",
		Description:   "Widget tracker",
		Fork:          false,
		URL:           "https://api.github.com/repos/acme/widgets",
		DefaultBranch: "main",
		Visibility:    "public",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

func issuesFixture() []github.IssueResponse {
	now := time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC)
	author := &github.UserResponse{ID: 21, NodeID: "U_author", Login: "octo", Type: "User", AvatarURL: "https://example.com/octo.png", HTMLURL: "https://github.com/octo", URL: "https://api.github.com/users/octo"}
	return []github.IssueResponse{
		{
			ID:        202,
			NodeID:    "I_kgDO2",
			Number:    2,
			Title:     "Fix parser",
			Body:      "Need to handle edge cases.",
			State:     "open",
			User:      author,
			Comments:  3,
			HTMLURL:   "https://github.com/acme/widgets/issues/2",
			URL:       "https://api.github.com/repos/acme/widgets/issues/2",
			CreatedAt: now.Add(1 * time.Hour),
			UpdatedAt: now.Add(2 * time.Hour),
			PullRequest: &github.IssuePullRequestRef{
				URL: "https://api.github.com/repos/acme/widgets/pulls/2",
			},
		},
		{
			ID:        201,
			NodeID:    "I_kgDO1",
			Number:    1,
			Title:     "Initial issue",
			Body:      "Track setup.",
			State:     "open",
			User:      author,
			Comments:  1,
			HTMLURL:   "https://github.com/acme/widgets/issues/1",
			URL:       "https://api.github.com/repos/acme/widgets/issues/1",
			CreatedAt: now,
			UpdatedAt: now,
		},
	}
}

func pullsFixture() []github.PullRequestResponse {
	now := time.Date(2026, 4, 14, 13, 0, 0, 0, time.UTC)
	author := &github.UserResponse{ID: 21, NodeID: "U_author", Login: "octo", Type: "User", AvatarURL: "https://example.com/octo.png", HTMLURL: "https://github.com/octo", URL: "https://api.github.com/users/octo"}
	baseRepo := github.PullBranchRepository{
		ID:            101,
		NodeID:        "R_kgDORepo",
		Name:          "widgets",
		FullName:      "acme/widgets",
		Private:       false,
		Owner:         &github.UserResponse{ID: 11, NodeID: "U_owner", Login: "acme", Type: "Organization", AvatarURL: "https://example.com/acme.png", HTMLURL: "https://github.com/acme", URL: "https://api.github.com/users/acme"},
		HTMLURL:       "https://github.com/acme/widgets",
		Description:   "Widget tracker",
		Fork:          false,
		URL:           "https://api.github.com/repos/acme/widgets",
		DefaultBranch: "main",
		Visibility:    "public",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	headRepo := baseRepo
	headRepo.FullName = "octo/widgets"
	headRepo.Owner = author
	headRepo.ID = 303
	headRepo.NodeID = "R_kgDOHead"
	headRepo.HTMLURL = "https://github.com/octo/widgets"
	headRepo.URL = "https://api.github.com/repos/octo/widgets"

	return []github.PullRequestResponse{
		{
			ID:             202,
			NodeID:         "PR_kgDO2",
			Number:         2,
			State:          "open",
			Title:          "Fix parser",
			Body:           "Need to handle edge cases.",
			User:           author,
			Draft:          false,
			Head:           github.PullBranch{Ref: "fix/parser", SHA: "abc123", Repo: &headRepo},
			Base:           github.PullBranch{Ref: "main", SHA: "def456", Repo: &baseRepo},
			Mergeable:      boolPtr(true),
			MergeableState: "clean",
			Merged:         false,
			Additions:      10,
			Deletions:      2,
			ChangedFiles:   1,
			Commits:        1,
			HTMLURL:        "https://github.com/acme/widgets/pull/2",
			URL:            "https://api.github.com/repos/acme/widgets/pulls/2",
			DiffURL:        "https://github.com/acme/widgets/pull/2.diff",
			PatchURL:       "https://github.com/acme/widgets/pull/2.patch",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
}

func boolPtr(value bool) *bool {
	return &value
}

func writeJSON(t *testing.T, w http.ResponseWriter, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(payload))
}
