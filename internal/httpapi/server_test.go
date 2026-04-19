package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/dutifuldev/ghreplica/internal/github"
	"github.com/dutifuldev/ghreplica/internal/githubsync"
	"github.com/dutifuldev/ghreplica/internal/gitindex"
	"github.com/dutifuldev/ghreplica/internal/httpapi"
	"github.com/dutifuldev/ghreplica/internal/testfixtures"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type stubChangeStatusProvider struct {
	repoStatus gitindex.RepoStatus
	repoErr    error
}

func (s stubChangeStatusProvider) GetRepoChangeStatus(ctx context.Context, owner, repo string) (gitindex.RepoStatus, error) {
	return s.repoStatus, s.repoErr
}

func (s stubChangeStatusProvider) GetPullRequestChangeStatus(ctx context.Context, owner, repo string, number int) (gitindex.PullRequestStatus, error) {
	return gitindex.PullRequestStatus{}, gorm.ErrRecordNotFound
}

func TestReadinessReturnsCheapDatabaseHealth(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	oldFailedAt := time.Now().UTC().Add(-2 * time.Hour)
	require.NoError(t, db.WithContext(ctx).Create(&database.RepositoryRefreshJob{
		FullName:    "acme/widgets",
		JobType:     "bootstrap_repository",
		Source:      "manual",
		Status:      "failed",
		MaxAttempts: 3,
		RequestedAt: oldFailedAt,
		FinishedAt:  &oldFailedAt,
		CreatedAt:   oldFailedAt,
		UpdatedAt:   oldFailedAt,
	}).Error)
	require.NoError(t, db.WithContext(ctx).Create(&database.RepositoryRefreshJob{
		FullName:    "acme/widgets",
		JobType:     "bootstrap_repository",
		Source:      "webhook",
		Status:      "superseded",
		MaxAttempts: 3,
		RequestedAt: oldFailedAt,
		FinishedAt:  &oldFailedAt,
		CreatedAt:   oldFailedAt,
		UpdatedAt:   oldFailedAt,
	}).Error)

	server := httpapi.NewServer(db, httpapi.Options{})
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Equal(t, "ready", payload["status"])
	require.Equal(t, "ok", payload["database"])
}

func TestMirrorStatusEndpoint(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	owner := &database.User{
		GitHubID:  11,
		Login:     "acme",
		Type:      "Organization",
		AvatarURL: "https://example.com/acme.png",
		HTMLURL:   "https://github.com/acme",
		APIURL:    "https://api.github.com/users/acme",
	}
	require.NoError(t, db.WithContext(ctx).Create(owner).Error)

	repo := &database.Repository{
		GitHubID:      101,
		OwnerID:       &owner.ID,
		OwnerLogin:    "acme",
		Name:          "widgets",
		FullName:      "acme/widgets",
		HTMLURL:       "https://github.com/acme/widgets",
		APIURL:        "https://api.github.com/repos/acme/widgets",
		DefaultBranch: "main",
		Visibility:    "public",
	}
	require.NoError(t, db.WithContext(ctx).Create(repo).Error)

	now := time.Now().UTC()
	require.NoError(t, db.WithContext(ctx).Create(&database.TrackedRepository{
		Owner:                    "acme",
		Name:                     "widgets",
		FullName:                 "acme/widgets",
		RepositoryID:             &repo.ID,
		SyncMode:                 "webhook_only",
		WebhookProjectionEnabled: true,
		AllowManualBackfill:      true,
		IssuesCompleteness:       "sparse",
		PullsCompleteness:        "sparse",
		CommentsCompleteness:     "sparse",
		ReviewsCompleteness:      "sparse",
		Enabled:                  true,
		LastWebhookAt:            &now,
	}).Error)

	issue := &database.Issue{
		RepositoryID:    repo.ID,
		GitHubID:        201,
		Number:          1,
		Title:           "Broken thing",
		State:           "open",
		CommentsCount:   1,
		HTMLURL:         "https://github.com/acme/widgets/issues/1",
		APIURL:          "https://api.github.com/repos/acme/widgets/issues/1",
		GitHubCreatedAt: now,
		GitHubUpdatedAt: now,
	}
	require.NoError(t, db.WithContext(ctx).Create(issue).Error)

	pull := &database.PullRequest{
		IssueID:         issue.ID,
		RepositoryID:    repo.ID,
		GitHubID:        202,
		Number:          1,
		State:           "open",
		HeadRef:         "fix/thing",
		HeadSHA:         "abc123",
		BaseRef:         "main",
		BaseSHA:         "def456",
		HTMLURL:         "https://github.com/acme/widgets/pull/1",
		APIURL:          "https://api.github.com/repos/acme/widgets/pulls/1",
		GitHubCreatedAt: now,
		GitHubUpdatedAt: now,
	}
	require.NoError(t, db.WithContext(ctx).Create(pull).Error)

	require.NoError(t, db.WithContext(ctx).Create(&database.IssueComment{
		GitHubID:        301,
		RepositoryID:    repo.ID,
		IssueID:         issue.ID,
		Body:            "I can reproduce this.",
		HTMLURL:         "https://github.com/acme/widgets/issues/1#issuecomment-301",
		APIURL:          "https://api.github.com/repos/acme/widgets/issues/comments/301",
		GitHubCreatedAt: now,
		GitHubUpdatedAt: now,
	}).Error)

	require.NoError(t, db.WithContext(ctx).Create(&database.PullRequestReview{
		GitHubID:        401,
		RepositoryID:    repo.ID,
		PullRequestID:   pull.IssueID,
		State:           "APPROVED",
		HTMLURL:         "https://github.com/acme/widgets/pull/1#pullrequestreview-401",
		APIURL:          "https://api.github.com/repos/acme/widgets/pulls/reviews/401",
		GitHubCreatedAt: now,
		GitHubUpdatedAt: now,
	}).Error)

	require.NoError(t, db.WithContext(ctx).Create(&database.PullRequestReviewComment{
		GitHubID:        501,
		RepositoryID:    repo.ID,
		PullRequestID:   pull.IssueID,
		Path:            "main.go",
		Body:            "Looks good.",
		HTMLURL:         "https://github.com/acme/widgets/pull/1#discussion_r501",
		APIURL:          "https://api.github.com/repos/acme/widgets/pulls/comments/501",
		PullRequestURL:  pull.APIURL,
		GitHubCreatedAt: now,
		GitHubUpdatedAt: now,
	}).Error)

	server := httpapi.NewServer(db, httpapi.Options{})
	req := httptest.NewRequest(http.MethodGet, "/v1/changes/repos/acme/widgets/mirror-status", nil)
	rec := httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Equal(t, "acme/widgets", payload["full_name"])
	require.Equal(t, "webhook_only", payload["sync_mode"])
	require.Equal(t, "sparse", payload["reviews_completeness"])
	counts := payload["counts"].(map[string]any)
	require.EqualValues(t, 1, counts["issues"])
	require.EqualValues(t, 1, counts["pulls"])
	require.EqualValues(t, 1, counts["issue_comments"])
	require.EqualValues(t, 1, counts["pull_request_reviews"])
	require.EqualValues(t, 1, counts["pull_request_review_comments"])
}

func TestMirrorRepositoryEndpoints(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	owner := &database.User{
		GitHubID: 11,
		Login:    "acme",
		Type:     "Organization",
	}
	require.NoError(t, db.WithContext(ctx).Create(owner).Error)

	repo := &database.Repository{
		GitHubID:      101,
		NodeID:        "R_kgDORepo",
		OwnerID:       &owner.ID,
		OwnerLogin:    "acme",
		Name:          "widgets",
		FullName:      "acme/widgets",
		DefaultBranch: "main",
		Visibility:    "public",
		Fork:          false,
	}
	require.NoError(t, db.WithContext(ctx).Create(repo).Error)

	now := time.Now().UTC()
	require.NoError(t, db.WithContext(ctx).Create(&database.TrackedRepository{
		Owner:                    "acme",
		Name:                     "widgets",
		FullName:                 "acme/widgets",
		RepositoryID:             &repo.ID,
		SyncMode:                 "webhook_only",
		WebhookProjectionEnabled: true,
		AllowManualBackfill:      true,
		IssuesCompleteness:       "sparse",
		PullsCompleteness:        "sparse",
		CommentsCompleteness:     "sparse",
		ReviewsCompleteness:      "sparse",
		Enabled:                  true,
		LastWebhookAt:            &now,
	}).Error)

	issue := &database.Issue{
		RepositoryID:    repo.ID,
		GitHubID:        201,
		Number:          1,
		Title:           "Broken thing",
		State:           "open",
		CommentsCount:   1,
		GitHubCreatedAt: now,
		GitHubUpdatedAt: now,
	}
	require.NoError(t, db.WithContext(ctx).Create(issue).Error)
	require.NoError(t, db.WithContext(ctx).Create(&database.PullRequest{
		IssueID:         issue.ID,
		RepositoryID:    repo.ID,
		GitHubID:        202,
		Number:          1,
		State:           "open",
		HeadRef:         "fix/thing",
		HeadSHA:         "abc123",
		BaseRef:         "main",
		BaseSHA:         "def456",
		GitHubCreatedAt: now,
		GitHubUpdatedAt: now,
	}).Error)

	server := httpapi.NewServer(db, httpapi.Options{
		ChangeStatus: stubChangeStatusProvider{
			repoStatus: gitindex.RepoStatus{
				FullName:                   "acme/widgets",
				TargetedRefreshPending:     true,
				InventoryNeedsRefresh:      true,
				BackfillRunning:            true,
				OpenPRTotal:                10,
				OpenPRCurrent:              6,
				OpenPRStale:                1,
				OpenPRMissing:              3,
				LastBackfillStartedAt:      &now,
				LastInventoryScanStartedAt: &now,
			},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/mirror/repos", nil)
	rec := httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var listed []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &listed))
	require.Len(t, listed, 1)
	require.Equal(t, "acme/widgets", listed[0]["full_name"])
	require.Equal(t, true, listed[0]["enabled"])
	require.NotContains(t, listed[0], "repository_id")
	require.NotContains(t, listed[0], "tracked_repository_present")
	require.Contains(t, listed[0], "completeness")
	require.Contains(t, listed[0], "timestamps")

	req = httptest.NewRequest(http.MethodGet, "/v1/mirror/repos/acme/widgets", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var mirrorRepo map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &mirrorRepo))
	require.Equal(t, "acme/widgets", mirrorRepo["full_name"])
	require.EqualValues(t, 101, mirrorRepo["github_id"])
	require.Equal(t, false, mirrorRepo["fork"])
	require.NotContains(t, mirrorRepo, "repository_present")
	require.NotContains(t, mirrorRepo, "tracked_repository_id")

	req = httptest.NewRequest(http.MethodGet, "/v1/mirror/repos/acme/widgets/status", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var status map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &status))
	require.Equal(t, "running", status["sync"].(map[string]any)["state"])
	require.EqualValues(t, 10, status["pull_request_changes"].(map[string]any)["total"])
	require.Equal(t, true, status["activity"].(map[string]any)["backfill_running"])
	require.NotContains(t, status, "inventory_generation_current")
	require.NotContains(t, status, "backfill_generation")
}

func TestMirrorRepositoryListPagination(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	for _, fullName := range []string{"acme/a", "acme/b", "acme/c"} {
		parts := strings.SplitN(fullName, "/", 2)
		require.NoError(t, db.WithContext(ctx).Create(&database.TrackedRepository{
			Owner:                parts[0],
			Name:                 parts[1],
			FullName:             fullName,
			SyncMode:             "webhook_only",
			IssuesCompleteness:   "sparse",
			PullsCompleteness:    "sparse",
			CommentsCompleteness: "sparse",
			ReviewsCompleteness:  "sparse",
			Enabled:              true,
		}).Error)
	}

	server := httpapi.NewServer(db, httpapi.Options{})
	req := httptest.NewRequest(http.MethodGet, "/v1/mirror/repos?page=2&per_page=1", nil)
	rec := httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Header().Get("Link"), `page=1`)
	require.Contains(t, rec.Header().Get("Link"), `page=3`)

	var payload []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Len(t, payload, 1)
	require.Equal(t, "acme/b", payload[0]["full_name"])
}

func TestMirrorStatusEndpointResolvesTrackedRepositoryAcrossRename(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	repo := &database.Repository{
		GitHubID:      101,
		OwnerLogin:    "acme",
		Name:          "widgets-renamed",
		FullName:      "acme/widgets-renamed",
		DefaultBranch: "main",
		Visibility:    "public",
	}
	require.NoError(t, db.WithContext(ctx).Create(repo).Error)

	require.NoError(t, db.WithContext(ctx).Create(&database.TrackedRepository{
		Owner:                    "acme",
		Name:                     "widgets",
		FullName:                 "acme/widgets",
		RepositoryID:             &repo.ID,
		SyncMode:                 "webhook_only",
		WebhookProjectionEnabled: true,
		AllowManualBackfill:      true,
		IssuesCompleteness:       "sparse",
		PullsCompleteness:        "sparse",
		CommentsCompleteness:     "sparse",
		ReviewsCompleteness:      "sparse",
		Enabled:                  true,
	}).Error)

	server := httpapi.NewServer(db, httpapi.Options{})
	req := httptest.NewRequest(http.MethodGet, "/v1/changes/repos/acme/widgets-renamed/mirror-status", nil)
	rec := httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Equal(t, "acme/widgets-renamed", payload["full_name"])
	require.Equal(t, true, payload["tracked_repository_present"])
	require.EqualValues(t, repo.ID, payload["repository_id"])
}

func TestGitHubLikeEndpointsExposeRealFixtureShapes(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	githubServer := httptest.NewServer(testfixtures.NewOpenClawGitHubHandler(t))
	t.Cleanup(githubServer.Close)

	service := githubsync.NewService(db, github.NewClient(githubServer.URL, github.AuthConfig{}))
	require.NoError(t, service.SyncIssue(ctx, "openclaw", "openclaw", 66797))
	require.NoError(t, service.SyncPullRequest(ctx, "openclaw", "openclaw", 66863))

	server := httpapi.NewServer(db, httpapi.Options{})

	req := httptest.NewRequest(http.MethodGet, "/v1/github/repos/openclaw/openclaw/pulls/66863", nil)
	rec := httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var pull map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &pull))
	head := pull["head"].(map[string]any)
	base := pull["base"].(map[string]any)
	require.Equal(t, "fix/whatsapp-connection-stability", head["ref"])
	require.Equal(t, "main", base["ref"])
	require.Equal(t, "Yellowfish23/openclaw", head["repo"].(map[string]any)["full_name"])
	require.Equal(t, "openclaw/openclaw", base["repo"].(map[string]any)["full_name"])
	require.Equal(t, "Yellowfish23", pull["user"].(map[string]any)["login"])

	req = httptest.NewRequest(http.MethodGet, "/v1/github/repos/openclaw/openclaw/pulls/66863/reviews", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var reviews []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &reviews))
	require.Len(t, reviews, 1)
	require.Equal(t, "greptile-apps[bot]", reviews[0]["user"].(map[string]any)["login"])

	req = httptest.NewRequest(http.MethodGet, "/v1/github/repos/openclaw/openclaw/pulls/66863/comments", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var reviewComments []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &reviewComments))
	require.Len(t, reviewComments, 2)
	require.Equal(t, "extensions/whatsapp/src/use-atomic-auth-state.ts", reviewComments[0]["path"])
	require.EqualValues(t, 204, reviewComments[0]["line"])

	req = httptest.NewRequest(http.MethodGet, "/v1/github/repos/openclaw/openclaw/issues/66797/comments", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var issueComments []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &issueComments))
	require.Len(t, issueComments, 1)
	require.Equal(t, "kpiyush88", issueComments[0]["user"].(map[string]any)["login"])
}

func TestGitHubExtensionBatchReadObjects(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	githubServer := httptest.NewServer(testfixtures.NewOpenClawGitHubHandler(t))
	t.Cleanup(githubServer.Close)

	service := githubsync.NewService(db, github.NewClient(githubServer.URL, github.AuthConfig{}))
	require.NoError(t, service.SyncIssue(ctx, "openclaw", "openclaw", 66797))
	require.NoError(t, service.SyncPullRequest(ctx, "openclaw", "openclaw", 66863))

	server := httpapi.NewServer(db, httpapi.Options{})

	body := []byte(`{
		"objects": [
			{"type": "issue", "number": 66797},
			{"type": "pull_request", "number": 66863},
			{"type": "pull_request", "number": 999999},
			{"type": "issue", "number": 66797}
		]
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/github-ext/repos/openclaw/openclaw/objects/batch", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var payload struct {
		Results []map[string]any `json:"results"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Len(t, payload.Results, 4)

	require.Equal(t, "issue", payload.Results[0]["type"])
	require.EqualValues(t, 66797, payload.Results[0]["number"])
	require.Equal(t, true, payload.Results[0]["found"])
	require.EqualValues(t, 66797, payload.Results[0]["object"].(map[string]any)["number"])

	require.Equal(t, "pull_request", payload.Results[1]["type"])
	require.EqualValues(t, 66863, payload.Results[1]["number"])
	require.Equal(t, true, payload.Results[1]["found"])
	require.EqualValues(t, 66863, payload.Results[1]["object"].(map[string]any)["number"])

	require.Equal(t, "pull_request", payload.Results[2]["type"])
	require.EqualValues(t, 999999, payload.Results[2]["number"])
	require.Equal(t, false, payload.Results[2]["found"])
	_, ok := payload.Results[2]["object"]
	require.False(t, ok)

	require.Equal(t, "issue", payload.Results[3]["type"])
	require.EqualValues(t, 66797, payload.Results[3]["number"])
	require.Equal(t, true, payload.Results[3]["found"])
	require.EqualValues(t, 66797, payload.Results[3]["object"].(map[string]any)["number"])
}

func TestGitHubExtensionBatchReadObjectsRejectsInvalidInput(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	githubServer := httptest.NewServer(testfixtures.NewOpenClawGitHubHandler(t))
	t.Cleanup(githubServer.Close)

	service := githubsync.NewService(db, github.NewClient(githubServer.URL, github.AuthConfig{}))
	require.NoError(t, service.SyncIssue(ctx, "openclaw", "openclaw", 66797))

	server := httpapi.NewServer(db, httpapi.Options{})

	req := httptest.NewRequest(http.MethodPost, "/v1/github-ext/repos/openclaw/openclaw/objects/batch", bytes.NewReader([]byte(`{
		"objects": [{"type": "commit", "number": 1}]
	}`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"message":"Unsupported object type"}`, rec.Body.String())
}

func testDatabaseURL(t *testing.T) string {
	t.Helper()
	return "sqlite://" + filepath.Join(t.TempDir(), "ghreplica-httpapi-test.db")
}
