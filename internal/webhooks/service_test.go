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
	"github.com/dutifuldev/ghreplica/internal/testfixtures"
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
		"action":       "opened",
		"repository":   repoFixture(),
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
	require.Equal(t, "webhook_only", tracked.SyncMode)
	require.False(t, tracked.AllowManualBackfill)
	require.Equal(t, "sparse", tracked.IssuesCompleteness)
	require.Equal(t, "sparse", tracked.PullsCompleteness)
	require.Equal(t, "empty", tracked.CommentsCompleteness)
	require.Equal(t, "empty", tracked.ReviewsCompleteness)

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

func TestWebhookIngestionReusesTrackedRepositoryAcrossRenameByRepositoryID(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	repo := &database.Repository{
		GitHubID:   101,
		OwnerLogin: "acme",
		Name:       "widgets",
		FullName:   "acme/widgets",
	}
	require.NoError(t, db.WithContext(ctx).Create(repo).Error)

	tracked := &database.TrackedRepository{
		Owner:        "acme",
		Name:         "widgets",
		FullName:     "acme/widgets",
		RepositoryID: &repo.ID,
		SyncMode:     "webhook_only",
		Enabled:      true,
	}
	require.NoError(t, db.WithContext(ctx).Create(tracked).Error)

	ingestor := webhooks.NewService(db, nil)
	err = ingestor.HandleWebhook(
		ctx,
		"delivery-rename-unsupported",
		"workflow_job",
		http.Header{"X-GitHub-Event": []string{"workflow_job"}},
		[]byte(`{"repository":{"id":101,"name":"widgets-renamed","full_name":"acme/widgets-renamed","owner":{"login":"acme"}}}`),
	)
	require.NoError(t, err)

	var trackedRows []database.TrackedRepository
	require.NoError(t, db.WithContext(ctx).Order("id ASC").Find(&trackedRows).Error)
	require.Len(t, trackedRows, 1)
	require.Equal(t, tracked.ID, trackedRows[0].ID)
	require.Equal(t, "widgets-renamed", trackedRows[0].Name)
	require.Equal(t, "acme/widgets-renamed", trackedRows[0].FullName)
	require.NotNil(t, trackedRows[0].RepositoryID)
	require.Equal(t, repo.ID, *trackedRows[0].RepositoryID)
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

	var tracked database.TrackedRepository
	require.NoError(t, db.WithContext(ctx).Where("full_name = ?", "acme/widgets").First(&tracked).Error)
	require.Equal(t, "sparse", tracked.IssuesCompleteness)
	require.Equal(t, "sparse", tracked.CommentsCompleteness)
}

func TestWebhookIngestionProjectsReviewAndReviewCommentPayloadsFromRealFixtures(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	projector := githubsync.NewService(db, github.NewClient("https://api.github.com", github.AuthConfig{}))
	ingestor := webhooks.NewService(db, projector)

	repo := testfixtures.OpenClawRepository(t)
	pull := testfixtures.OpenClawPull66863(t)
	review := testfixtures.OpenClawPull66863Reviews(t)[0]
	reviewComment := testfixtures.OpenClawPull66863ReviewComments(t)[0]

	reviewPayload, err := json.Marshal(map[string]any{
		"action":       "submitted",
		"repository":   repo,
		"pull_request": pull,
		"review":       review,
	})
	require.NoError(t, err)
	require.NoError(t, ingestor.HandleWebhook(ctx, "delivery-review", "pull_request_review", http.Header{"X-GitHub-Event": []string{"pull_request_review"}}, reviewPayload))

	reviewCommentPayload, err := json.Marshal(map[string]any{
		"action":       "created",
		"repository":   repo,
		"pull_request": pull,
		"comment":      reviewComment,
	})
	require.NoError(t, err)
	require.NoError(t, ingestor.HandleWebhook(ctx, "delivery-review-comment", "pull_request_review_comment", http.Header{"X-GitHub-Event": []string{"pull_request_review_comment"}}, reviewCommentPayload))

	var storedRepo database.Repository
	require.NoError(t, db.WithContext(ctx).Where("full_name = ?", "openclaw/openclaw").First(&storedRepo).Error)

	var reviews int64
	var reviewComments int64
	require.NoError(t, db.WithContext(ctx).Model(&database.PullRequestReview{}).Where("repository_id = ?", storedRepo.ID).Count(&reviews).Error)
	require.NoError(t, db.WithContext(ctx).Model(&database.PullRequestReviewComment{}).Where("repository_id = ?", storedRepo.ID).Count(&reviewComments).Error)
	require.EqualValues(t, 1, reviews)
	require.EqualValues(t, 1, reviewComments)

	var tracked database.TrackedRepository
	require.NoError(t, db.WithContext(ctx).Where("full_name = ?", "openclaw/openclaw").First(&tracked).Error)
	require.Equal(t, "sparse", tracked.PullsCompleteness)
	require.Equal(t, "sparse", tracked.ReviewsCompleteness)
	require.Equal(t, "sparse", tracked.CommentsCompleteness)
}

func TestWebhookIngestionUpsertsIssueCommentAcrossDeliveries(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	projector := githubsync.NewService(db, github.NewClient("https://api.github.com", github.AuthConfig{}))
	ingestor := webhooks.NewService(db, projector)

	repo := testfixtures.OpenClawRepository(t)
	issue := testfixtures.OpenClawIssue66797(t)
	comment := testfixtures.OpenClawIssue66797Comments(t)[0]

	payload, err := json.Marshal(map[string]any{
		"action":     "created",
		"repository": repo,
		"issue":      issue,
		"comment":    comment,
	})
	require.NoError(t, err)
	require.NoError(t, ingestor.HandleWebhook(ctx, "delivery-issue-comment-1", "issue_comment", http.Header{"X-GitHub-Event": []string{"issue_comment"}}, payload))

	edited := comment
	edited.Body = "Updated issue comment body from an edited delivery."
	payload, err = json.Marshal(map[string]any{
		"action":     "edited",
		"repository": repo,
		"issue":      issue,
		"comment":    edited,
	})
	require.NoError(t, err)
	require.NoError(t, ingestor.HandleWebhook(ctx, "delivery-issue-comment-2", "issue_comment", http.Header{"X-GitHub-Event": []string{"issue_comment"}}, payload))

	var comments []database.IssueComment
	require.NoError(t, db.WithContext(ctx).Order("github_id ASC").Find(&comments).Error)
	require.Len(t, comments, 1)
	require.Equal(t, edited.Body, comments[0].Body)
}

func TestWebhookIngestionDeletesIssueAndReviewComments(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	projector := githubsync.NewService(db, github.NewClient("https://api.github.com", github.AuthConfig{}))
	ingestor := webhooks.NewService(db, projector)

	repo := testfixtures.OpenClawRepository(t)
	issue := testfixtures.OpenClawIssue66797(t)
	issueComment := testfixtures.OpenClawIssue66797Comments(t)[0]

	payload, err := json.Marshal(map[string]any{
		"action":     "created",
		"repository": repo,
		"issue":      issue,
		"comment":    issueComment,
	})
	require.NoError(t, err)
	require.NoError(t, ingestor.HandleWebhook(ctx, "delivery-issue-comment-create", "issue_comment", http.Header{"X-GitHub-Event": []string{"issue_comment"}}, payload))

	payload, err = json.Marshal(map[string]any{
		"action":     "deleted",
		"repository": repo,
		"issue":      issue,
		"comment":    issueComment,
	})
	require.NoError(t, err)
	require.NoError(t, ingestor.HandleWebhook(ctx, "delivery-issue-comment-delete", "issue_comment", http.Header{"X-GitHub-Event": []string{"issue_comment"}}, payload))

	pullIssue := testfixtures.OpenClawIssue66863(t)
	pull := testfixtures.OpenClawPull66863(t)
	reviewComment := testfixtures.OpenClawPull66863ReviewComments(t)[0]

	payload, err = json.Marshal(map[string]any{
		"action":       "created",
		"repository":   repo,
		"pull_request": pull,
		"comment":      reviewComment,
	})
	require.NoError(t, err)
	require.NoError(t, ingestor.HandleWebhook(ctx, "delivery-review-comment-create", "pull_request_review_comment", http.Header{"X-GitHub-Event": []string{"pull_request_review_comment"}}, payload))

	payload, err = json.Marshal(map[string]any{
		"action":       "deleted",
		"repository":   repo,
		"pull_request": pull,
		"comment":      reviewComment,
	})
	require.NoError(t, err)
	require.NoError(t, ingestor.HandleWebhook(ctx, "delivery-review-comment-delete", "pull_request_review_comment", http.Header{"X-GitHub-Event": []string{"pull_request_review_comment"}}, payload))

	var issueComments int64
	var reviewComments int64
	require.NoError(t, db.WithContext(ctx).Model(&database.IssueComment{}).Count(&issueComments).Error)
	require.NoError(t, db.WithContext(ctx).Model(&database.PullRequestReviewComment{}).Count(&reviewComments).Error)
	require.Zero(t, issueComments)
	require.Zero(t, reviewComments)

	var issueRows int64
	require.NoError(t, db.WithContext(ctx).Model(&database.Issue{}).Where("number IN ?", []int{issue.Number, pullIssue.Number}).Count(&issueRows).Error)
	require.EqualValues(t, 2, issueRows)
}

func TestWebhookIngestionProjectsClosedIssuePayloadFromRealFixture(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	projector := githubsync.NewService(db, github.NewClient("https://api.github.com", github.AuthConfig{}))
	ingestor := webhooks.NewService(db, projector)

	payload, err := json.Marshal(map[string]any{
		"action":     "closed",
		"repository": testfixtures.OpenClawRepository(t),
		"issue":      testfixtures.OpenClawIssue67094Closed(t),
	})
	require.NoError(t, err)
	require.NoError(t, ingestor.HandleWebhook(
		ctx,
		"delivery-issue-closed",
		"issues",
		http.Header{"X-GitHub-Event": []string{"issues"}},
		payload,
	))

	var issue database.Issue
	require.NoError(t, db.WithContext(ctx).Where("github_id = ?", int64(4267632693)).First(&issue).Error)
	require.Equal(t, 67094, issue.Number)
	require.Equal(t, "closed", issue.State)
	require.Equal(t, "not_planned", issue.StateReason)

	var tracked database.TrackedRepository
	require.NoError(t, db.WithContext(ctx).Where("full_name = ?", "openclaw/openclaw").First(&tracked).Error)
	require.Equal(t, "webhook_only", tracked.SyncMode)
	require.Equal(t, "sparse", tracked.IssuesCompleteness)
}

func TestWebhookIngestionProjectsPullRequestActionMatrixFromRealFixtures(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	projector := githubsync.NewService(db, github.NewClient("https://api.github.com", github.AuthConfig{}))
	ingestor := webhooks.NewService(db, projector)

	repo := testfixtures.OpenClawRepository(t)
	openPull := testfixtures.OpenClawPull67096Open(t)
	closedPull := testfixtures.OpenClawPull67079Closed(t)

	for _, tc := range []struct {
		deliveryID string
		action     string
		pull       github.PullRequestResponse
	}{
		{deliveryID: "delivery-pr-sync", action: "synchronize", pull: openPull},
		{deliveryID: "delivery-pr-ready", action: "ready_for_review", pull: openPull},
		{deliveryID: "delivery-pr-closed", action: "closed", pull: closedPull},
	} {
		payload, err := json.Marshal(map[string]any{
			"action":       tc.action,
			"repository":   repo,
			"pull_request": tc.pull,
		})
		require.NoError(t, err)
		require.NoError(t, ingestor.HandleWebhook(
			ctx,
			tc.deliveryID,
			"pull_request",
			http.Header{"X-GitHub-Event": []string{"pull_request"}},
			payload,
		))
	}

	var pulls []database.PullRequest
	require.NoError(t, db.WithContext(ctx).Order("number asc").Find(&pulls).Error)
	require.Len(t, pulls, 2)
	require.Equal(t, 67079, pulls[0].Number)
	require.Equal(t, "closed", pulls[0].State)
	require.Equal(t, "fix/66975-telegram-commands-registry-caching", pulls[0].HeadRef)
	require.Equal(t, 67096, pulls[1].Number)
	require.Equal(t, "open", pulls[1].State)
	require.Equal(t, "ci/upgrade-v4-actions", pulls[1].HeadRef)

	var tracked database.TrackedRepository
	require.NoError(t, db.WithContext(ctx).Where("full_name = ?", "openclaw/openclaw").First(&tracked).Error)
	require.Equal(t, "sparse", tracked.IssuesCompleteness)
	require.Equal(t, "sparse", tracked.PullsCompleteness)
}

func TestWebhookIngestionSynchronizeQueuesTargetedRefreshWithoutDirtyingInventory(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	projector := githubsync.NewService(db, github.NewClient("https://api.github.com", github.AuthConfig{}))
	ingestor := webhooks.NewService(db, projector)

	repo := testfixtures.OpenClawRepository(t)
	pull := testfixtures.OpenClawPull67096Open(t)
	payload, err := json.Marshal(map[string]any{
		"action":       "synchronize",
		"repository":   repo,
		"pull_request": pull,
	})
	require.NoError(t, err)
	require.NoError(t, ingestor.HandleWebhook(
		ctx,
		"delivery-pr-sync-targeted-only",
		"pull_request",
		http.Header{"X-GitHub-Event": []string{"pull_request"}},
		payload,
	))

	var storedRepo database.Repository
	require.NoError(t, db.WithContext(ctx).Where("github_id = ?", repo.ID).First(&storedRepo).Error)

	var state database.RepoChangeSyncState
	require.NoError(t, db.WithContext(ctx).Where("repository_id = ?", storedRepo.ID).First(&state).Error)
	require.False(t, state.Dirty)
	require.Nil(t, state.DirtySince)
	require.NotNil(t, state.LastWebhookAt)

	var refresh database.RepoTargetedPullRefresh
	require.NoError(t, db.WithContext(ctx).
		Where("repository_id = ? AND pull_request_number = ?", storedRepo.ID, pull.Number).
		First(&refresh).Error)
	require.NotNil(t, refresh.RequestedAt)
	require.NotNil(t, refresh.LastWebhookAt)
}

func TestWebhookIngestionMarksBaseBranchPushesAsInventoryRefreshWork(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	projector := githubsync.NewService(db, github.NewClient("https://api.github.com", github.AuthConfig{}))
	ingestor := webhooks.NewService(db, projector)

	repo := testfixtures.OpenClawRepository(t)
	storedRepo, err := projector.UpsertRepository(ctx, repo)
	require.NoError(t, err)
	require.NoError(t, db.WithContext(ctx).Create(&database.PullRequestChangeSnapshot{
		RepositoryID:      storedRepo.ID,
		PullRequestNumber: 67096,
		BaseRef:           "main",
		IndexFreshness:    "current",
		HeadSHA:           "abc123",
		BaseSHA:           "def456",
	}).Error)

	payload, err := json.Marshal(map[string]any{
		"ref":        "refs/heads/main",
		"repository": repo,
	})
	require.NoError(t, err)
	require.NoError(t, ingestor.HandleWebhook(
		ctx,
		"delivery-push-main",
		"push",
		http.Header{"X-GitHub-Event": []string{"push"}},
		payload,
	))

	var state database.RepoChangeSyncState
	require.NoError(t, db.WithContext(ctx).Where("repository_id = ?", storedRepo.ID).First(&state).Error)
	require.True(t, state.Dirty)
	require.NotNil(t, state.DirtySince)
}

func TestWebhookIngestionReplaysReviewAndReviewCommentEditsWithoutDuplicates(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	projector := githubsync.NewService(db, github.NewClient("https://api.github.com", github.AuthConfig{}))
	ingestor := webhooks.NewService(db, projector)

	repo := testfixtures.OpenClawRepository(t)
	pull := testfixtures.OpenClawPull66863(t)
	review := testfixtures.OpenClawPull66863Reviews(t)[0]
	reviewComment := testfixtures.OpenClawPull66863ReviewComments(t)[0]

	reviewPayload, err := json.Marshal(map[string]any{
		"action":       "submitted",
		"repository":   repo,
		"pull_request": pull,
		"review":       review,
	})
	require.NoError(t, err)
	require.NoError(t, ingestor.HandleWebhook(ctx, "delivery-review-submit", "pull_request_review", http.Header{"X-GitHub-Event": []string{"pull_request_review"}}, reviewPayload))

	reviewPayload, err = json.Marshal(map[string]any{
		"action":       "edited",
		"repository":   repo,
		"pull_request": pull,
		"review":       review,
	})
	require.NoError(t, err)
	require.NoError(t, ingestor.HandleWebhook(ctx, "delivery-review-edit", "pull_request_review", http.Header{"X-GitHub-Event": []string{"pull_request_review"}}, reviewPayload))

	reviewCommentPayload, err := json.Marshal(map[string]any{
		"action":       "created",
		"repository":   repo,
		"pull_request": pull,
		"comment":      reviewComment,
	})
	require.NoError(t, err)
	require.NoError(t, ingestor.HandleWebhook(ctx, "delivery-review-comment-create", "pull_request_review_comment", http.Header{"X-GitHub-Event": []string{"pull_request_review_comment"}}, reviewCommentPayload))

	reviewCommentPayload, err = json.Marshal(map[string]any{
		"action":       "edited",
		"repository":   repo,
		"pull_request": pull,
		"comment":      reviewComment,
	})
	require.NoError(t, err)
	require.NoError(t, ingestor.HandleWebhook(ctx, "delivery-review-comment-edit", "pull_request_review_comment", http.Header{"X-GitHub-Event": []string{"pull_request_review_comment"}}, reviewCommentPayload))

	var reviews int64
	var reviewComments int64
	require.NoError(t, db.WithContext(ctx).Model(&database.PullRequestReview{}).Count(&reviews).Error)
	require.NoError(t, db.WithContext(ctx).Model(&database.PullRequestReviewComment{}).Count(&reviewComments).Error)
	require.EqualValues(t, 1, reviews)
	require.EqualValues(t, 1, reviewComments)
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
