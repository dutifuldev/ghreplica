package githubsync_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/dutifuldev/ghreplica/internal/github"
	"github.com/dutifuldev/ghreplica/internal/githubsync"
	"github.com/dutifuldev/ghreplica/internal/httpapi"
	"github.com/dutifuldev/ghreplica/internal/testfixtures"
	"github.com/stretchr/testify/require"
)

func TestBootstrapRepositoryAndServeGitHubLikeEndpoints(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	githubServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/widgets":
			writeJSON(t, w, repoFixture())
		case "/repos/acme/widgets/issues":
			writeJSON(t, w, issuesFixture())
		case "/repos/acme/widgets/issues/1":
			writeJSON(t, w, issuesFixture()[1])
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

	service := githubsync.NewService(db, github.NewClient(githubServer.URL, github.AuthConfig{}))
	require.NoError(t, service.BootstrapRepository(ctx, "acme", "widgets"))

	server := httpapi.NewServer(db, httpapi.Options{})

	req := httptest.NewRequest(http.MethodGet, "/v1/github/repos/acme/widgets", nil)
	rec := httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var repo map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &repo))
	require.Equal(t, "acme/widgets", repo["full_name"])

	req = httptest.NewRequest(http.MethodGet, "/v1/github/repos/acme/widgets/issues?state=all", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var issues []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &issues))
	require.Len(t, issues, 2)
	require.Equal(t, "Fix parser", issues[0]["title"])
	require.NotNil(t, issues[0]["pull_request"])

	req = httptest.NewRequest(http.MethodGet, "/v1/github/repos/acme/widgets/pulls?state=all", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var pulls []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &pulls))
	require.Len(t, pulls, 1)
	require.Equal(t, "Fix parser", pulls[0]["title"])
	require.Equal(t, "fix/parser", pulls[0]["head"].(map[string]any)["ref"])

	req = httptest.NewRequest(http.MethodGet, "/v1/github/repos/acme/widgets/issues/2", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	req = httptest.NewRequest(http.MethodGet, "/v1/github/repos/acme/widgets/issues/2/comments", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var issueComments []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &issueComments))
	require.Len(t, issueComments, 1)

	req = httptest.NewRequest(http.MethodGet, "/v1/github/repos/acme/widgets/pulls/2", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	req = httptest.NewRequest(http.MethodGet, "/v1/github/repos/acme/widgets/pulls/2/reviews", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var reviews []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &reviews))
	require.Len(t, reviews, 1)

	req = httptest.NewRequest(http.MethodGet, "/v1/github/repos/acme/widgets/pulls/2/comments", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var reviewComments []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &reviewComments))
	require.Len(t, reviewComments, 1)
}

func TestBootstrapRepositoryNormalizesWebhookAliasSyncMode(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))
	require.NoError(t, db.Create(&database.TrackedRepository{
		Owner:    "acme",
		Name:     "widgets",
		FullName: "acme/widgets",
		SyncMode: "webhook",
		Enabled:  true,
	}).Error)

	githubServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/widgets":
			writeJSON(t, w, repoFixture())
		case "/repos/acme/widgets/issues":
			writeJSON(t, w, issuesFixture())
		case "/repos/acme/widgets/issues/1":
			writeJSON(t, w, issuesFixture()[1])
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

	service := githubsync.NewService(db, github.NewClient(githubServer.URL, github.AuthConfig{}))
	require.NoError(t, service.BootstrapRepository(ctx, "acme", "widgets"))

	var tracked database.TrackedRepository
	require.NoError(t, db.Where("full_name = ?", "acme/widgets").First(&tracked).Error)
	require.Equal(t, "webhook_only", tracked.SyncMode)
	require.True(t, tracked.WebhookProjectionEnabled)
	require.True(t, tracked.AllowManualBackfill)
	require.Equal(t, "backfilled", tracked.PullsCompleteness)
}

func TestUpsertRepositoryTracksRenameByGitHubID(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	service := githubsync.NewService(db, github.NewClient("https://api.github.com", github.AuthConfig{}))
	first, err := service.UpsertRepository(ctx, repoFixture())
	require.NoError(t, err)

	renamed := repoFixture()
	renamed.Name = "widgets-renamed"
	renamed.FullName = "acme/widgets-renamed"
	renamed.HTMLURL = "https://github.com/acme/widgets-renamed"
	renamed.URL = "https://api.github.com/repos/acme/widgets-renamed"

	second, err := service.UpsertRepository(ctx, renamed)
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)

	var repos []database.Repository
	require.NoError(t, db.Order("id ASC").Find(&repos).Error)
	require.Len(t, repos, 1)
	require.Equal(t, "widgets-renamed", repos[0].Name)
	require.Equal(t, "acme/widgets-renamed", repos[0].FullName)
}

func TestUpsertRepositoryReclaimsFullNameFromDifferentGitHubID(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	service := githubsync.NewService(db, github.NewClient("https://api.github.com", github.AuthConfig{}))

	stale := repoFixture()
	stale.ID = 999001
	stale.NodeID = "R_stale"
	require.NoError(t, db.Create(&database.Repository{
		GitHubID:      stale.ID,
		NodeID:        stale.NodeID,
		OwnerLogin:    "acme",
		Name:          "widgets",
		FullName:      stale.FullName,
		Private:       stale.Private,
		Archived:      stale.Archived,
		Disabled:      stale.Disabled,
		DefaultBranch: stale.DefaultBranch,
		Description:   stale.Description,
		HTMLURL:       stale.HTMLURL,
		APIURL:        stale.URL,
		Visibility:    stale.Visibility,
		Fork:          stale.Fork,
		CreatedAt:     stale.CreatedAt,
		UpdatedAt:     stale.UpdatedAt,
	}).Error)

	current, err := service.UpsertRepository(ctx, repoFixture())
	require.NoError(t, err)
	require.EqualValues(t, repoFixture().ID, current.GitHubID)
	require.Equal(t, repoFixture().FullName, current.FullName)

	var repos []database.Repository
	require.NoError(t, db.Order("github_id ASC").Find(&repos).Error)
	require.Len(t, repos, 2)

	byGitHubID := make(map[int64]database.Repository, len(repos))
	for _, repo := range repos {
		byGitHubID[repo.GitHubID] = repo
	}

	staleStored, ok := byGitHubID[stale.ID]
	require.True(t, ok)
	require.NotEqual(t, repoFixture().FullName, staleStored.FullName)
	require.Contains(t, staleStored.FullName, "__ghreplica_released__/")

	currentStored, ok := byGitHubID[repoFixture().ID]
	require.True(t, ok)
	require.Equal(t, repoFixture().FullName, currentStored.FullName)
}

func TestRefreshOpenPullInventoryNow(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	repository := database.Repository{
		GitHubID:   101,
		OwnerLogin: "acme",
		Name:       "widgets",
		FullName:   "acme/widgets",
	}
	require.NoError(t, db.Create(&repository).Error)
	require.NoError(t, db.Create(&database.RepoChangeSyncState{
		RepositoryID: repository.ID,
		Dirty:        true,
		BackfillMode: "open_only",
	}).Error)

	var listPullCount int
	githubServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/acme/widgets/pulls" {
			listPullCount++
			writeJSON(t, w, pullsFixture())
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(githubServer.Close)

	service := githubsync.NewService(db, github.NewClient(githubServer.URL, github.AuthConfig{}))
	result, err := service.RefreshOpenPullInventoryNow(ctx, "acme", "widgets", time.Minute)
	require.NoError(t, err)
	require.Equal(t, len(pullsFixture()), result.OpenPRTotal)
	require.Equal(t, len(pullsFixture()), result.OpenPRMissing)
	require.Equal(t, 1, listPullCount)

	status, err := service.GetRepoChangeStatus(ctx, "acme", "widgets")
	require.NoError(t, err)
	require.Equal(t, 1, status.InventoryGenerationCurrent)
	require.NotNil(t, status.InventoryLastCommittedAt)
	require.False(t, status.InventoryScanRunning)
	require.Equal(t, len(pullsFixture()), status.OpenPRTotal)
	require.Equal(t, len(pullsFixture()), status.OpenPRMissing)
	require.Empty(t, status.LastError)
}

func TestRefreshOpenPullInventoryNowRejectsActiveRepoPhase(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	repository := database.Repository{
		GitHubID:   102,
		OwnerLogin: "acme",
		Name:       "widgets",
		FullName:   "acme/widgets",
	}
	require.NoError(t, db.Create(&repository).Error)

	now := time.Now().UTC()
	until := now.Add(time.Minute)
	require.NoError(t, db.Create(&database.RepoChangeSyncState{
		RepositoryID:             repository.ID,
		BackfillMode:             "open_only",
		LastBackfillStartedAt:    &now,
		BackfillLeaseHeartbeatAt: &now,
		BackfillLeaseUntil:       &until,
	}).Error)

	var listPullCount int
	githubServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/acme/widgets/pulls" {
			listPullCount++
			writeJSON(t, w, pullsFixture())
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(githubServer.Close)

	service := githubsync.NewService(db, github.NewClient(githubServer.URL, github.AuthConfig{}))
	_, err = service.RefreshOpenPullInventoryNow(ctx, "acme", "widgets", time.Minute)
	require.EqualError(t, err, "cannot refresh inventory while backfill is running for acme/widgets")
	require.Zero(t, listPullCount)
}

func TestRefreshOpenPullInventoryNowAllowsCompatibleRepoPhase(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	repository := database.Repository{
		GitHubID:   103,
		OwnerLogin: "acme",
		Name:       "widgets",
		FullName:   "acme/widgets",
	}
	require.NoError(t, db.Create(&repository).Error)

	now := time.Now().UTC()
	until := now.Add(time.Minute)
	require.NoError(t, db.Create(&database.RepoChangeSyncState{
		RepositoryID:                    repository.ID,
		BackfillMode:                    "open_only",
		InventoryGenerationCurrent:      1,
		TargetedRefreshLeaseHeartbeatAt: &now,
		TargetedRefreshLeaseUntil:       &until,
	}).Error)

	var listPullCount int
	githubServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/acme/widgets/pulls" {
			listPullCount++
			writeJSON(t, w, pullsFixture())
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(githubServer.Close)

	service := githubsync.NewService(db, github.NewClient(githubServer.URL, github.AuthConfig{}))
	result, err := service.RefreshOpenPullInventoryNow(ctx, "acme", "widgets", time.Minute)
	require.NoError(t, err)
	require.Equal(t, 1, result.OpenPRTotal)
	require.Equal(t, 1, listPullCount)
}

func TestUpsertsMaintainSearchDocuments(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	service := githubsync.NewService(db, github.NewClient("https://api.github.com", github.AuthConfig{}))

	repo, err := service.UpsertRepository(ctx, repoFixture())
	require.NoError(t, err)

	_, err = service.UpsertIssue(ctx, repo.ID, issuesFixture()[1])
	require.NoError(t, err)
	require.NoError(t, service.UpsertPullRequest(ctx, repo.ID, pullsFixture()[0]))
	require.NoError(t, service.UpsertIssueComment(ctx, repo.ID, issueCommentsFixture()[0]))
	require.NoError(t, service.UpsertPullRequestReview(ctx, repo.ID, 2, pullReviewsFixture()[0]))
	require.NoError(t, service.UpsertPullRequestReviewComment(ctx, repo.ID, 2, pullReviewCommentsFixture()[0]))

	var docs []database.SearchDocument
	require.NoError(t, db.WithContext(ctx).Where("repository_id = ?", repo.ID).Order("document_type ASC").Find(&docs).Error)
	require.Len(t, docs, 5)
	require.Equal(t, "issue", docs[0].DocumentType)
	require.Equal(t, "issue_comment", docs[1].DocumentType)
	require.Equal(t, "pull_request", docs[2].DocumentType)
	require.Equal(t, "pull_request_review", docs[3].DocumentType)
	require.Equal(t, "pull_request_review_comment", docs[4].DocumentType)
}

func TestUpsertIssueStripsNullBytesFromProjectedTextAndRawJSON(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	service := githubsync.NewService(db, github.NewClient("https://api.github.com", github.AuthConfig{}))

	repo, err := service.UpsertRepository(ctx, repoFixture())
	require.NoError(t, err)

	issue := issuesFixture()[1]
	issue.Title = "Fix\x00 parser"
	issue.Body = "line 1\x00line 2"

	stored, err := service.UpsertIssue(ctx, repo.ID, issue)
	require.NoError(t, err)
	require.Equal(t, "Fix parser", stored.Title)
	require.Equal(t, "line 1line 2", stored.Body)
	require.NotContains(t, string(stored.RawJSON), "\\u0000")
	require.Contains(t, string(stored.RawJSON), "\"body\":\"line 1line 2\"")
}

func TestUpsertIssuePreservesLiteralUnicodeEscapeMarkersInRawJSON(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	service := githubsync.NewService(db, github.NewClient("https://api.github.com", github.AuthConfig{}))

	repo, err := service.UpsertRepository(ctx, repoFixture())
	require.NoError(t, err)

	issue := issuesFixture()[1]
	issue.Body = `literal \u0000 marker`

	stored, err := service.UpsertIssue(ctx, repo.ID, issue)
	require.NoError(t, err)
	require.Equal(t, `literal \u0000 marker`, stored.Body)
	require.Contains(t, string(stored.RawJSON), `\\u0000`)
	require.Contains(t, string(stored.RawJSON), `"body":"literal \\u0000 marker"`)
}

func TestTargetedSyncIssueAndPullRequest(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	githubServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/widgets":
			writeJSON(t, w, repoFixture())
		case "/repos/acme/widgets/issues/2":
			writeJSON(t, w, issuesFixture()[0])
		case "/repos/acme/widgets/issues/2/comments":
			writeJSON(t, w, issueCommentsFixture())
		case "/repos/acme/widgets/pulls/2":
			writeJSON(t, w, pullsFixture()[0])
		case "/repos/acme/widgets/pulls/2/reviews":
			writeJSON(t, w, pullReviewsFixture())
		case "/repos/acme/widgets/pulls/2/comments":
			writeJSON(t, w, pullReviewCommentsFixture())
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(githubServer.Close)

	service := githubsync.NewService(db, github.NewClient(githubServer.URL, github.AuthConfig{}))
	require.NoError(t, service.SyncIssue(ctx, "acme", "widgets", 2))
	require.NoError(t, service.SyncPullRequest(ctx, "acme", "widgets", 2))

	var tracked database.TrackedRepository
	require.NoError(t, db.Where("full_name = ?", "acme/widgets").First(&tracked).Error)
	require.Equal(t, "webhook_only", tracked.SyncMode)
	require.Equal(t, "sparse", tracked.ReviewsCompleteness)
	require.Equal(t, "sparse", tracked.CommentsCompleteness)

	var issues int64
	var pulls int64
	var issueComments int64
	var reviews int64
	var reviewComments int64
	require.NoError(t, db.Model(&database.Issue{}).Count(&issues).Error)
	require.NoError(t, db.Model(&database.PullRequest{}).Count(&pulls).Error)
	require.NoError(t, db.Model(&database.IssueComment{}).Count(&issueComments).Error)
	require.NoError(t, db.Model(&database.PullRequestReview{}).Count(&reviews).Error)
	require.NoError(t, db.Model(&database.PullRequestReviewComment{}).Count(&reviewComments).Error)
	require.EqualValues(t, 1, issues)
	require.EqualValues(t, 1, pulls)
	require.EqualValues(t, 1, issueComments)
	require.EqualValues(t, 1, reviews)
	require.EqualValues(t, 1, reviewComments)
}

func TestSyncIssuePreservesExistingTrackedCompletenessAndTimestamps(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	githubServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/widgets":
			writeJSON(t, w, repoFixture())
		case "/repos/acme/widgets/issues/2":
			writeJSON(t, w, issuesFixture()[0])
		case "/repos/acme/widgets/issues/2/comments":
			writeJSON(t, w, issueCommentsFixture())
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(githubServer.Close)

	service := githubsync.NewService(db, github.NewClient(githubServer.URL, github.AuthConfig{}))
	repo, err := service.UpsertRepository(ctx, repoFixture())
	require.NoError(t, err)

	bootstrapAt := time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC)
	crawlAt := bootstrapAt.Add(2 * time.Hour)
	webhookAt := bootstrapAt.Add(4 * time.Hour)
	require.NoError(t, db.WithContext(ctx).Create(&database.TrackedRepository{
		Owner:                    "acme",
		Name:                     "widgets",
		FullName:                 "acme/widgets",
		RepositoryID:             &repo.ID,
		SyncMode:                 "webhook_only",
		WebhookProjectionEnabled: true,
		AllowManualBackfill:      true,
		IssuesCompleteness:       "sparse",
		PullsCompleteness:        "backfilled",
		CommentsCompleteness:     "backfilled",
		ReviewsCompleteness:      "backfilled",
		Enabled:                  true,
		LastBootstrapAt:          &bootstrapAt,
		LastCrawlAt:              &crawlAt,
		LastWebhookAt:            &webhookAt,
	}).Error)

	require.NoError(t, service.SyncIssue(ctx, "acme", "widgets", 2))

	var tracked database.TrackedRepository
	require.NoError(t, db.WithContext(ctx).Where("repository_id = ?", repo.ID).First(&tracked).Error)
	require.Equal(t, "sparse", tracked.IssuesCompleteness)
	require.Equal(t, "backfilled", tracked.PullsCompleteness)
	require.Equal(t, "sparse", tracked.CommentsCompleteness)
	require.Equal(t, "backfilled", tracked.ReviewsCompleteness)
	require.Equal(t, bootstrapAt, tracked.LastBootstrapAt.UTC())
	require.Equal(t, crawlAt, tracked.LastCrawlAt.UTC())
	require.Equal(t, webhookAt, tracked.LastWebhookAt.UTC())
}

func TestTargetedSyncWithRealFixturesPersistsDiscussionData(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	githubServer := httptest.NewServer(testfixtures.NewOpenClawGitHubHandler(t))
	t.Cleanup(githubServer.Close)

	service := githubsync.NewService(db, github.NewClient(githubServer.URL, github.AuthConfig{}))
	require.NoError(t, service.SyncIssue(ctx, "openclaw", "openclaw", 66797))
	require.NoError(t, service.SyncPullRequest(ctx, "openclaw", "openclaw", 66863))

	var repo database.Repository
	require.NoError(t, db.Where("full_name = ?", "openclaw/openclaw").First(&repo).Error)

	var tracked database.TrackedRepository
	require.NoError(t, db.Where("full_name = ?", "openclaw/openclaw").First(&tracked).Error)
	require.Equal(t, "webhook_only", tracked.SyncMode)
	require.Equal(t, "sparse", tracked.IssuesCompleteness)
	require.Equal(t, "sparse", tracked.PullsCompleteness)
	require.Equal(t, "sparse", tracked.CommentsCompleteness)
	require.Equal(t, "sparse", tracked.ReviewsCompleteness)

	var issues int64
	var pulls int64
	var issueComments int64
	var reviews int64
	var reviewComments int64
	require.NoError(t, db.Model(&database.Issue{}).Where("repository_id = ?", repo.ID).Count(&issues).Error)
	require.NoError(t, db.Model(&database.PullRequest{}).Where("repository_id = ?", repo.ID).Count(&pulls).Error)
	require.NoError(t, db.Model(&database.IssueComment{}).Where("repository_id = ?", repo.ID).Count(&issueComments).Error)
	require.NoError(t, db.Model(&database.PullRequestReview{}).Where("repository_id = ?", repo.ID).Count(&reviews).Error)
	require.NoError(t, db.Model(&database.PullRequestReviewComment{}).Where("repository_id = ?", repo.ID).Count(&reviewComments).Error)
	require.EqualValues(t, 2, issues)
	require.EqualValues(t, 1, pulls)
	require.EqualValues(t, 2, issueComments)
	require.EqualValues(t, 1, reviews)
	require.EqualValues(t, 2, reviewComments)

	server := httpapi.NewServer(db, httpapi.Options{})

	req := httptest.NewRequest(http.MethodGet, "/v1/github/repos/openclaw/openclaw/pulls/66863/reviews", nil)
	rec := httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var apiReviews []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &apiReviews))
	require.Len(t, apiReviews, 1)
	require.Equal(t, "greptile-apps[bot]", apiReviews[0]["user"].(map[string]any)["login"])
	require.Equal(t, "COMMENTED", apiReviews[0]["state"])

	req = httptest.NewRequest(http.MethodGet, "/v1/github/repos/openclaw/openclaw/pulls/66863/comments", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var apiReviewComments []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &apiReviewComments))
	require.Len(t, apiReviewComments, 2)
	require.Equal(t, "extensions/whatsapp/src/use-atomic-auth-state.ts", apiReviewComments[0]["path"])
	require.EqualValues(t, 204, apiReviewComments[0]["line"])
}

func testDatabaseURL(t *testing.T) string {
	t.Helper()
	return "sqlite://file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
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
