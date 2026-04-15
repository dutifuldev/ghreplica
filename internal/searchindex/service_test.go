package searchindex_test

import (
	"context"
	"testing"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/dutifuldev/ghreplica/internal/searchindex"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRebuildRepositoryAndSearchMentions(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	repo := seedSearchFixtures(t, db)
	service := searchindex.NewService(db)
	require.NoError(t, service.RebuildRepositoryByID(ctx, repo.ID))

	var docs int64
	require.NoError(t, db.WithContext(ctx).Model(&database.SearchDocument{}).Where("repository_id = ?", repo.ID).Count(&docs).Error)
	require.EqualValues(t, 7, docs)

	matches, err := service.SearchMentions(ctx, repo.ID, searchindex.MentionRequest{
		Query: "heartbeat watchdog",
		Mode:  searchindex.ModeFTS,
		Limit: 10,
		Page:  1,
	})
	require.NoError(t, err)
	require.Len(t, matches, 5)
	require.Equal(t, searchindex.DocumentTypePullRequest, matches[0].Resource.Type)
	require.Equal(t, "title", matches[0].MatchedField)

	matches, err = service.SearchMentions(ctx, repo.ID, searchindex.MentionRequest{
		Query:  "watch dog",
		Mode:   searchindex.ModeFuzzy,
		Scopes: []string{searchindex.ScopeIssues, searchindex.ScopePullRequests},
		Limit:  10,
		Page:   1,
	})
	require.NoError(t, err)
	require.NotEmpty(t, matches)
	require.Contains(t, []string{searchindex.DocumentTypeIssue, searchindex.DocumentTypePullRequest}, matches[0].Resource.Type)

	matches, err = service.SearchMentions(ctx, repo.ID, searchindex.MentionRequest{
		Query:  "watchdog.*variable",
		Mode:   searchindex.ModeRegex,
		Scopes: []string{searchindex.ScopePullRequestReviewComments},
		Limit:  10,
		Page:   1,
	})
	require.NoError(t, err)
	require.Len(t, matches, 1)
	require.Equal(t, searchindex.DocumentTypePullRequestReviewComment, matches[0].Resource.Type)
	require.Equal(t, "body", matches[0].MatchedField)

	matches, err = service.SearchMentions(ctx, repo.ID, searchindex.MentionRequest{
		Query:  "heartbeat",
		Mode:   searchindex.ModeFTS,
		Author: "reviewer",
		Limit:  10,
		Page:   1,
	})
	require.NoError(t, err)
	require.Len(t, matches, 1)
	require.Equal(t, searchindex.DocumentTypePullRequestReview, matches[0].Resource.Type)

	matches, err = service.SearchMentions(ctx, repo.ID, searchindex.MentionRequest{
		Query:  "auth state",
		Mode:   searchindex.ModeFTS,
		Scopes: []string{searchindex.ScopePullRequests},
		State:  "closed",
		Limit:  10,
		Page:   1,
	})
	require.NoError(t, err)
	require.Len(t, matches, 1)
	require.Equal(t, searchindex.DocumentTypePullRequest, matches[0].Resource.Type)
	require.Equal(t, 11, matches[0].Resource.Number)
}

func TestSearchMentionsRejectsInvalidRequests(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	repo := seedSearchFixtures(t, db)
	service := searchindex.NewService(db)
	require.NoError(t, service.RebuildRepositoryByID(ctx, repo.ID))

	_, err = service.SearchMentions(ctx, repo.ID, searchindex.MentionRequest{})
	require.Error(t, err)
	require.True(t, searchindex.IsInvalidRequest(err))

	_, err = service.SearchMentions(ctx, repo.ID, searchindex.MentionRequest{Query: "(", Mode: searchindex.ModeRegex})
	require.Error(t, err)
	require.True(t, searchindex.IsInvalidRequest(err))
}

func TestGetRepoStatusLifecycle(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	repo := seedSearchFixtures(t, db)
	service := searchindex.NewService(db)

	status, err := service.GetRepoStatus(ctx, "acme", "widgets")
	require.NoError(t, err)
	require.Equal(t, searchindex.TextIndexStatusMissing, status.TextIndexStatus)
	require.EqualValues(t, 0, status.DocumentCount)
	require.Equal(t, searchindex.TextIndexFreshnessUnknown, status.Freshness)
	require.Equal(t, searchindex.TextIndexCoverageEmpty, status.Coverage)

	require.NoError(t, service.RebuildRepositoryByID(ctx, repo.ID))

	status, err = service.GetRepoStatus(ctx, "acme", "widgets")
	require.NoError(t, err)
	require.Equal(t, searchindex.TextIndexStatusReady, status.TextIndexStatus)
	require.EqualValues(t, 7, status.DocumentCount)
	require.Equal(t, searchindex.TextIndexFreshnessCurrent, status.Freshness)
	require.Equal(t, searchindex.TextIndexCoverageComplete, status.Coverage)
	require.NotNil(t, status.LastIndexedAt)
	require.NotNil(t, status.LastSourceUpdateAt)
	require.Empty(t, status.LastError)

	var issue database.Issue
	require.NoError(t, db.WithContext(ctx).Where("repository_id = ? AND number = ?", repo.ID, 1).First(&issue).Error)
	issue.Body = "The heartbeat watchdog now drops ACP messages after reconnect and retry."
	issue.GitHubUpdatedAt = issue.GitHubUpdatedAt.Add(30 * time.Minute)
	require.NoError(t, db.WithContext(ctx).Save(&issue).Error)
	require.NoError(t, service.UpsertIssue(ctx, issue))

	statusAfterUpsert, err := service.GetRepoStatus(ctx, "acme", "widgets")
	require.NoError(t, err)
	require.Equal(t, searchindex.TextIndexStatusReady, statusAfterUpsert.TextIndexStatus)
	require.Equal(t, searchindex.TextIndexFreshnessCurrent, statusAfterUpsert.Freshness)
	require.Equal(t, searchindex.TextIndexCoverageComplete, statusAfterUpsert.Coverage)
	require.NotNil(t, statusAfterUpsert.LastIndexedAt)
	require.NotNil(t, statusAfterUpsert.LastSourceUpdateAt)
	require.True(t, statusAfterUpsert.LastSourceUpdateAt.Equal(issue.GitHubUpdatedAt.UTC()) || statusAfterUpsert.LastSourceUpdateAt.After(issue.GitHubUpdatedAt.UTC()))
	require.True(t, statusAfterUpsert.LastIndexedAt.Equal(*status.LastIndexedAt) || statusAfterUpsert.LastIndexedAt.After(*status.LastIndexedAt))
}

func seedSearchFixtures(t *testing.T, db *gorm.DB) database.Repository {
	t.Helper()
	now := time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC)

	owner := database.User{GitHubID: 1, Login: "acme", Type: "Organization"}
	require.NoError(t, db.Create(&owner).Error)
	author := database.User{GitHubID: 2, Login: "octocat", Type: "User"}
	require.NoError(t, db.Create(&author).Error)
	reviewer := database.User{GitHubID: 3, Login: "reviewer", Type: "User"}
	require.NoError(t, db.Create(&reviewer).Error)

	repo := database.Repository{
		GitHubID:      101,
		OwnerID:       &owner.ID,
		OwnerLogin:    "acme",
		Name:          "widgets",
		FullName:      "acme/widgets",
		DefaultBranch: "main",
		APIURL:        "https://api.github.com/repos/acme/widgets",
		HTMLURL:       "https://github.com/acme/widgets",
	}
	require.NoError(t, db.Create(&repo).Error)

	issue1 := database.Issue{
		RepositoryID:    repo.ID,
		GitHubID:        201,
		Number:          1,
		Title:           "Heartbeat watchdog drops ACP messages",
		Body:            "The heartbeat watchdog silently drops ACP messages on reconnect.",
		State:           "open",
		AuthorID:        &author.ID,
		CommentsCount:   1,
		HTMLURL:         "https://github.com/acme/widgets/issues/1",
		APIURL:          "https://api.github.com/repos/acme/widgets/issues/1",
		GitHubCreatedAt: now,
		GitHubUpdatedAt: now.Add(1 * time.Minute),
	}
	require.NoError(t, db.Create(&issue1).Error)

	issue2 := database.Issue{
		RepositoryID:    repo.ID,
		GitHubID:        202,
		Number:          2,
		Title:           "Atomic auth state fails after reconnect",
		Body:            "Need to preserve auth state after reconnect.",
		State:           "closed",
		AuthorID:        &author.ID,
		CommentsCount:   0,
		HTMLURL:         "https://github.com/acme/widgets/issues/2",
		APIURL:          "https://api.github.com/repos/acme/widgets/issues/2",
		GitHubCreatedAt: now.Add(2 * time.Minute),
		GitHubUpdatedAt: now.Add(3 * time.Minute),
	}
	require.NoError(t, db.Create(&issue2).Error)

	prIssue10 := database.Issue{
		RepositoryID:      repo.ID,
		GitHubID:          301,
		Number:            10,
		Title:             "feat(acp): retry heartbeat watchdog",
		Body:              "Add heartbeat retry logic for ACP sessions and reconnect handling.",
		State:             "open",
		AuthorID:          &author.ID,
		CommentsCount:     2,
		IsPullRequest:     true,
		PullRequestAPIURL: "https://api.github.com/repos/acme/widgets/pulls/10",
		HTMLURL:           "https://github.com/acme/widgets/pull/10",
		APIURL:            "https://api.github.com/repos/acme/widgets/issues/10",
		GitHubCreatedAt:   now.Add(4 * time.Minute),
		GitHubUpdatedAt:   now.Add(5 * time.Minute),
	}
	require.NoError(t, db.Create(&prIssue10).Error)

	pr10 := database.PullRequest{
		IssueID:         prIssue10.ID,
		RepositoryID:    repo.ID,
		GitHubID:        302,
		Number:          10,
		State:           "open",
		HeadRef:         "feat/heartbeat-watchdog",
		HeadSHA:         "abc123",
		BaseRef:         "main",
		BaseSHA:         "def456",
		HTMLURL:         "https://github.com/acme/widgets/pull/10",
		APIURL:          "https://api.github.com/repos/acme/widgets/pulls/10",
		DiffURL:         "https://github.com/acme/widgets/pull/10.diff",
		PatchURL:        "https://github.com/acme/widgets/pull/10.patch",
		GitHubCreatedAt: now.Add(4 * time.Minute),
		GitHubUpdatedAt: now.Add(6 * time.Minute),
	}
	require.NoError(t, db.Create(&pr10).Error)

	prIssue11 := database.Issue{
		RepositoryID:      repo.ID,
		GitHubID:          303,
		Number:            11,
		Title:             "fix(whatsapp): atomic auth state restore",
		Body:              "Restore atomic auth state after disconnect.",
		State:             "closed",
		AuthorID:          &author.ID,
		CommentsCount:     0,
		IsPullRequest:     true,
		PullRequestAPIURL: "https://api.github.com/repos/acme/widgets/pulls/11",
		HTMLURL:           "https://github.com/acme/widgets/pull/11",
		APIURL:            "https://api.github.com/repos/acme/widgets/issues/11",
		GitHubCreatedAt:   now.Add(7 * time.Minute),
		GitHubUpdatedAt:   now.Add(8 * time.Minute),
	}
	require.NoError(t, db.Create(&prIssue11).Error)

	pr11 := database.PullRequest{
		IssueID:         prIssue11.ID,
		RepositoryID:    repo.ID,
		GitHubID:        304,
		Number:          11,
		State:           "closed",
		HeadRef:         "fix/auth-state",
		HeadSHA:         "ghi789",
		BaseRef:         "main",
		BaseSHA:         "def456",
		HTMLURL:         "https://github.com/acme/widgets/pull/11",
		APIURL:          "https://api.github.com/repos/acme/widgets/pulls/11",
		DiffURL:         "https://github.com/acme/widgets/pull/11.diff",
		PatchURL:        "https://github.com/acme/widgets/pull/11.patch",
		GitHubCreatedAt: now.Add(7 * time.Minute),
		GitHubUpdatedAt: now.Add(9 * time.Minute),
	}
	require.NoError(t, db.Create(&pr11).Error)

	issueComment := database.IssueComment{
		GitHubID:        401,
		RepositoryID:    repo.ID,
		IssueID:         issue1.ID,
		AuthorID:        &author.ID,
		Body:            "Still broken in the heartbeat worker after reconnect.",
		HTMLURL:         "https://github.com/acme/widgets/issues/1#issuecomment-401",
		APIURL:          "https://api.github.com/repos/acme/widgets/issues/comments/401",
		GitHubCreatedAt: now.Add(10 * time.Minute),
		GitHubUpdatedAt: now.Add(10 * time.Minute),
	}
	require.NoError(t, db.Create(&issueComment).Error)

	review := database.PullRequestReview{
		GitHubID:        501,
		RepositoryID:    repo.ID,
		PullRequestID:   pr10.IssueID,
		AuthorID:        &reviewer.ID,
		State:           "COMMENTED",
		Body:            "Heartbeat retry logic looks good to me.",
		CommitID:        "abc123",
		HTMLURL:         "https://github.com/acme/widgets/pull/10#pullrequestreview-501",
		APIURL:          "https://api.github.com/repos/acme/widgets/pulls/10/reviews/501",
		GitHubCreatedAt: now.Add(11 * time.Minute),
		GitHubUpdatedAt: now.Add(11 * time.Minute),
	}
	require.NoError(t, db.Create(&review).Error)

	reviewComment := database.PullRequestReviewComment{
		GitHubID:        601,
		RepositoryID:    repo.ID,
		PullRequestID:   pr10.IssueID,
		ReviewID:        &review.ID,
		AuthorID:        &reviewer.ID,
		Path:            "worker.go",
		Body:            "Please rename the watchdog variable before merge.",
		HTMLURL:         "https://github.com/acme/widgets/pull/10#discussion_r601",
		APIURL:          "https://api.github.com/repos/acme/widgets/pulls/comments/601",
		PullRequestURL:  "https://api.github.com/repos/acme/widgets/pulls/10",
		GitHubCreatedAt: now.Add(12 * time.Minute),
		GitHubUpdatedAt: now.Add(12 * time.Minute),
	}
	require.NoError(t, db.Create(&reviewComment).Error)

	return repo
}

func testDatabaseURL(t *testing.T) string {
	t.Helper()
	return "sqlite://" + t.TempDir() + "/searchindex.db"
}
