package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/dutifuldev/ghreplica/internal/httpapi"
	"github.com/stretchr/testify/require"
)

func TestReadinessIgnoresHistoricalFailedJobsAndSupersededJobs(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open("sqlite://file::memory:?cache=shared")
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

	db, err := database.Open("sqlite://file::memory:?cache=shared")
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
	req := httptest.NewRequest(http.MethodGet, "/repos/acme/widgets/_ghreplica", nil)
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
