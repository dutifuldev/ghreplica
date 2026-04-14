package webhooks_test

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/dutifuldev/ghreplica/internal/github"
	"github.com/dutifuldev/ghreplica/internal/githubsync"
	"github.com/dutifuldev/ghreplica/internal/webhooks"
	"github.com/stretchr/testify/require"
)

func TestWebhookIngestionProjectsPullRequestPayloadIntoCanonicalTables(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	projector := githubsync.NewService(db, github.NewClient("https://api.github.com", github.AuthConfig{}))
	ingestor := webhooks.NewService(db, projector)
	payload, err := json.Marshal(map[string]any{
		"action":      "opened",
		"repository":  repoFixture(),
		"pull_request": pullsFixture()[0],
	})
	require.NoError(t, err)

	err = ingestor.HandleWebhook(
		ctx,
		"delivery-1",
		"pull_request",
		http.Header{"X-GitHub-Event": []string{"pull_request"}},
		payload,
	)
	require.NoError(t, err)

	var delivery database.WebhookDelivery
	require.NoError(t, db.WithContext(ctx).Where("delivery_id = ?", "delivery-1").First(&delivery).Error)
	require.Equal(t, "pull_request", delivery.Event)
	require.NotNil(t, delivery.ProcessedAt)
	require.NotNil(t, delivery.RepositoryID)

	var repo database.Repository
	require.NoError(t, db.WithContext(ctx).Where("full_name = ?", "acme/widgets").First(&repo).Error)

	var issue database.Issue
	require.NoError(t, db.WithContext(ctx).Where("repository_id = ? AND number = ?", repo.ID, 2).First(&issue).Error)
	require.True(t, issue.IsPullRequest)

	var pull database.PullRequest
	require.NoError(t, db.WithContext(ctx).Where("repository_id = ? AND number = ?", repo.ID, 2).First(&pull).Error)
	require.Equal(t, "fix/parser", pull.HeadRef)

	var tracked database.TrackedRepository
	require.NoError(t, db.WithContext(ctx).Where("full_name = ?", "acme/widgets").First(&tracked).Error)
	require.NotNil(t, tracked.RepositoryID)
	require.Equal(t, repo.ID, *tracked.RepositoryID)

	var jobs int64
	require.NoError(t, db.WithContext(ctx).Model(&database.RepositoryRefreshJob{}).Count(&jobs).Error)
	require.Zero(t, jobs)
}

func TestWebhookIngestionIgnoresUnsupportedEventsForRefreshScheduling(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	projector := githubsync.NewService(db, github.NewClient("https://api.github.com", github.AuthConfig{}))
	ingestor := webhooks.NewService(db, projector)
	err = ingestor.HandleWebhook(
		ctx,
		"delivery-unsupported",
		"workflow_job",
		http.Header{"X-GitHub-Event": []string{"workflow_job"}},
		[]byte(`{"repository":{"name":"widgets","full_name":"acme/widgets","owner":{"login":"acme"}}}`),
	)
	require.NoError(t, err)

	var delivery database.WebhookDelivery
	require.NoError(t, db.WithContext(ctx).Where("delivery_id = ?", "delivery-unsupported").First(&delivery).Error)
	require.Equal(t, "workflow_job", delivery.Event)
	require.NotNil(t, delivery.ProcessedAt)

	var jobs int64
	require.NoError(t, db.WithContext(ctx).Model(&database.RepositoryRefreshJob{}).Count(&jobs).Error)
	require.Zero(t, jobs)

	var tracked int64
	require.NoError(t, db.WithContext(ctx).Model(&database.TrackedRepository{}).Count(&tracked).Error)
	require.EqualValues(t, 1, tracked)
}

func TestWebhookIngestionProjectsIssueCommentPayload(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	projector := githubsync.NewService(db, github.NewClient("https://api.github.com", github.AuthConfig{}))
	ingestor := webhooks.NewService(db, projector)
	payload, err := json.Marshal(map[string]any{
		"action":     "created",
		"repository": repoFixture(),
		"issue":      issuesFixture()[0],
		"comment":    issueCommentsFixture()[0],
	})
	require.NoError(t, err)

	err = ingestor.HandleWebhook(
		ctx,
		"delivery-comment",
		"issue_comment",
		http.Header{"X-GitHub-Event": []string{"issue_comment"}},
		payload,
	)
	require.NoError(t, err)

	var repo database.Repository
	require.NoError(t, db.WithContext(ctx).Where("full_name = ?", "acme/widgets").First(&repo).Error)

	var comments int64
	require.NoError(t, db.WithContext(ctx).Model(&database.IssueComment{}).Where("repository_id = ?", repo.ID).Count(&comments).Error)
	require.EqualValues(t, 1, comments)
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
			Head:           github.PullBranch{Ref: "fix/parser", SHA: "abc123", Repo: &baseRepo},
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

func issueCommentsFixture() []github.IssueCommentResponse {
	now := time.Date(2026, 4, 14, 14, 0, 0, 0, time.UTC)
	author := &github.UserResponse{ID: 21, NodeID: "U_author", Login: "octo", Type: "User", AvatarURL: "https://example.com/octo.png", HTMLURL: "https://github.com/octo", URL: "https://api.github.com/users/octo"}
	return []github.IssueCommentResponse{{
		ID:        301,
		NodeID:    "IC_kwDO301",
		Body:      "Looks good",
		User:      author,
		IssueURL:  "https://api.github.com/repos/acme/widgets/issues/2",
		HTMLURL:   "https://github.com/acme/widgets/issues/2#issuecomment-301",
		URL:       "https://api.github.com/repos/acme/widgets/issues/comments/301",
		CreatedAt: now,
		UpdatedAt: now,
	}}
}

func pullReviewsFixture() []github.PullRequestReviewResponse {
	now := time.Date(2026, 4, 14, 15, 0, 0, 0, time.UTC)
	author := &github.UserResponse{ID: 31, NodeID: "U_reviewer", Login: "reviewer", Type: "User", AvatarURL: "https://example.com/reviewer.png", HTMLURL: "https://github.com/reviewer", URL: "https://api.github.com/users/reviewer"}
	return []github.PullRequestReviewResponse{{
		ID:          401,
		NodeID:      "PRR_kwDO401",
		User:        author,
		Body:        "Approved",
		State:       "APPROVED",
		HTMLURL:     "https://github.com/acme/widgets/pull/2#pullrequestreview-401",
		URL:         "https://api.github.com/repos/acme/widgets/pulls/2/reviews/401",
		CommitID:    "abc123",
		SubmittedAt: &now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}}
}

func pullReviewCommentsFixture() []github.PullRequestReviewCommentResponse {
	now := time.Date(2026, 4, 14, 15, 5, 0, 0, time.UTC)
	author := &github.UserResponse{ID: 31, NodeID: "U_reviewer", Login: "reviewer", Type: "User", AvatarURL: "https://example.com/reviewer.png", HTMLURL: "https://github.com/reviewer", URL: "https://api.github.com/users/reviewer"}
	reviewID := int64(401)
	line := 12
	return []github.PullRequestReviewCommentResponse{{
		ID:                  501,
		NodeID:              "PRRC_kwDO501",
		PullRequestURL:      "https://api.github.com/repos/acme/widgets/pulls/2",
		PullRequestReviewID: &reviewID,
		HTMLURL:             "https://github.com/acme/widgets/pull/2#discussion_r501",
		URL:                 "https://api.github.com/repos/acme/widgets/pulls/comments/501",
		Body:                "Please rename this variable",
		Path:                "parser.go",
		DiffHunk:            "@@ -1,1 +1,1 @@",
		Line:                &line,
		OriginalLine:        &line,
		Side:                "RIGHT",
		User:                author,
		CreatedAt:           now,
		UpdatedAt:           now,
	}}
}

func boolPtr(value bool) *bool {
	return &value
}

func writeJSON(t *testing.T, w http.ResponseWriter, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(payload))
}

func testDatabaseURL(t *testing.T) string {
	t.Helper()
	return "sqlite://" + filepath.Join(t.TempDir(), "webhooks.db")
}
