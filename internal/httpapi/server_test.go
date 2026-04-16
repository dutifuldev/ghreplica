package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/dutifuldev/ghreplica/internal/github"
	"github.com/dutifuldev/ghreplica/internal/githubsync"
	"github.com/dutifuldev/ghreplica/internal/httpapi"
	"github.com/dutifuldev/ghreplica/internal/testfixtures"
	"github.com/stretchr/testify/require"
)

func TestReadinessIgnoresHistoricalFailedJobsAndSupersededJobs(t *testing.T) {
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
	require.EqualValues(t, 0, payload["recent_failed_jobs"])
	require.EqualValues(t, 1, payload["failed_jobs_total"])
	require.EqualValues(t, 1, payload["superseded_jobs"])
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

func testDatabaseURL(t *testing.T) string {
	t.Helper()
	return "sqlite://" + filepath.Join(t.TempDir(), "ghreplica-httpapi-test.db")
}
