package webhooks_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/dutifuldev/ghreplica/internal/github"
	"github.com/dutifuldev/ghreplica/internal/githubsync"
	"github.com/dutifuldev/ghreplica/internal/refresh"
	"github.com/dutifuldev/ghreplica/internal/webhooks"
	"github.com/stretchr/testify/require"
)

func TestWebhookIngestionQueuesRefreshAndWorkerMirrorsRepository(t *testing.T) {
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
		case "/repos/acme/widgets/issues/2":
			writeJSON(t, w, issuesFixture()[0])
		case "/repos/acme/widgets/pulls":
			writeJSON(t, w, pullsFixture())
		case "/repos/acme/widgets/pulls/2":
			writeJSON(t, w, pullsFixture()[0])
		case "/repos/acme/widgets/issues/comments":
			writeJSON(t, w, issueCommentsFixture())
		case "/repos/acme/widgets/pulls/2/reviews":
			writeJSON(t, w, pullReviewsFixture())
		case "/repos/acme/widgets/pulls/2/comments":
			writeJSON(t, w, pullReviewCommentsFixture())
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(githubServer.Close)

	scheduler := refresh.NewScheduler(db)
	ingestor := webhooks.NewService(db, scheduler)
	err = ingestor.HandleWebhook(
		ctx,
		"delivery-1",
		"ping",
		http.Header{"X-GitHub-Event": []string{"ping"}},
		[]byte(`{"repository":{"name":"widgets","full_name":"acme/widgets","owner":{"login":"acme"}}}`),
	)
	require.NoError(t, err)

	var delivery database.WebhookDelivery
	require.NoError(t, db.WithContext(ctx).Where("delivery_id = ?", "delivery-1").First(&delivery).Error)
	require.Equal(t, "ping", delivery.Event)
	require.NotNil(t, delivery.ProcessedAt)

	var job database.RepositoryRefreshJob
	require.NoError(t, db.WithContext(ctx).Where("full_name = ?", "acme/widgets").First(&job).Error)
	require.Equal(t, "pending", job.Status)

	bootstrap := githubsync.NewService(db, github.NewClient(githubServer.URL, github.AuthConfig{}))
	worker := refresh.NewWorker(db, bootstrap, time.Millisecond)
	processed, err := worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)

	require.NoError(t, db.WithContext(ctx).First(&job, job.ID).Error)
	require.Equal(t, "succeeded", job.Status)
	require.NotNil(t, job.FinishedAt)

	var repo database.Repository
	require.NoError(t, db.WithContext(ctx).Where("full_name = ?", "acme/widgets").First(&repo).Error)

	var tracked database.TrackedRepository
	require.NoError(t, db.WithContext(ctx).Where("full_name = ?", "acme/widgets").First(&tracked).Error)
	require.NotNil(t, tracked.RepositoryID)
	require.Equal(t, repo.ID, *tracked.RepositoryID)
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
