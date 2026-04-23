package searchindex

import (
	"context"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDocumentLifecycleAndStatusHelpers(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open("sqlite://" + filepath.Join(t.TempDir(), "searchindex-coverage.db"))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	repo, issue, pull, comment, review, reviewComment := seedSearchIndexCoverageData(t, db)
	service := NewService(db)

	require.NoError(t, service.UpsertIssue(ctx, issue))
	require.NoError(t, service.UpsertPullRequest(ctx, pull))
	require.NoError(t, service.UpsertIssueComment(ctx, comment))
	require.NoError(t, service.UpsertPullRequestReview(ctx, review))
	require.NoError(t, service.UpsertPullRequestReviewComment(ctx, reviewComment))

	var count int64
	require.NoError(t, db.WithContext(ctx).Model(&database.SearchDocument{}).Where("repository_id = ?", repo.ID).Count(&count).Error)
	require.EqualValues(t, 5, count)

	require.NoError(t, service.DeleteByGitHubID(ctx, repo.ID, DocumentTypeIssueComment, comment.GitHubID))
	require.NoError(t, db.WithContext(ctx).Model(&database.SearchDocument{}).Where("repository_id = ?", repo.ID).Count(&count).Error)
	require.EqualValues(t, 4, count)

	issue.IsPullRequest = true
	require.NoError(t, service.UpsertIssue(ctx, issue))
	require.NoError(t, service.RebuildRepository(ctx, "acme", "widgets"))

	status, err := service.GetRepoStatus(ctx, "acme", "widgets")
	require.NoError(t, err)
	require.Equal(t, TextIndexStatusReady, status.TextIndexStatus)
	require.Equal(t, TextIndexCoverageComplete, status.Coverage)

	require.NoError(t, service.markRebuildFailed(ctx, repo.ID, time.Now().UTC(), assertErr{}))
	status, err = service.GetRepoStatus(ctx, "acme", "widgets")
	require.NoError(t, err)
	require.Equal(t, TextIndexStatusFailed, status.TextIndexStatus)

	require.Equal(t, TextIndexStatusReady, normalizeTextIndexStatus("ready"))
	require.Equal(t, TextIndexStatusMissing, normalizeTextIndexStatus("unknown"))
	require.Equal(t, TextIndexFreshnessCurrent, normalizeTextIndexFreshness("current"))
	require.Equal(t, TextIndexFreshnessUnknown, normalizeTextIndexFreshness("bad"))
	require.Equal(t, TextIndexCoverageComplete, normalizeTextIndexCoverage("complete"))
	require.Equal(t, TextIndexCoverageEmpty, normalizeTextIndexCoverage("bad"))
	require.Equal(t, TextIndexCoverageComplete, coverageOrDefault(TextIndexCoverageEmpty, TextIndexCoverageComplete))

	now := time.Now().UTC()
	later := now.Add(time.Minute)
	require.Equal(t, TextIndexFreshnessCurrent, deriveFreshness(&later, &now))
	require.Equal(t, TextIndexFreshnessStale, deriveFreshness(&now, &later))
	require.Equal(t, later, normalizeSourceUpdatedAt(later, time.Time{}))
	require.Equal(t, later, *maxTimePtr(&now, later))
}

func TestDocumentBuilders(t *testing.T) {
	now := time.Now().UTC()
	author := &database.User{GitHubID: 1, Login: "octocat"}
	repo := database.Repository{ID: 1, FullName: "acme/widgets"}

	issueDoc, ok := buildIssueDocument(database.Issue{
		RepositoryID:    repo.ID,
		GitHubID:        11,
		Number:          7,
		Title:           "Issue title",
		Body:            "Issue body",
		State:           "open",
		Author:          author,
		HTMLURL:         "https://github.com/acme/widgets/issues/7",
		GitHubUpdatedAt: now,
	})
	require.True(t, ok)
	require.Equal(t, DocumentTypeIssue, issueDoc.DocumentType)

	pullIssue := database.Issue{
		ID:              2,
		RepositoryID:    repo.ID,
		GitHubID:        12,
		Number:          8,
		Title:           "Pull title",
		Body:            "Pull body",
		State:           "open",
		Author:          author,
		HTMLURL:         "https://github.com/acme/widgets/pull/8",
		GitHubUpdatedAt: now,
	}
	pullDoc, ok := buildPullRequestDocument(database.PullRequest{
		IssueID:         pullIssue.ID,
		Issue:           pullIssue,
		RepositoryID:    repo.ID,
		GitHubID:        21,
		Number:          8,
		HeadRef:         "feature",
		BaseRef:         "main",
		State:           "open",
		HTMLURL:         "https://github.com/acme/widgets/pull/8",
		GitHubUpdatedAt: now,
	})
	require.True(t, ok)
	require.Equal(t, DocumentTypePullRequest, pullDoc.DocumentType)

	commentDoc, ok := buildIssueCommentDocument(database.IssueComment{
		RepositoryID:    repo.ID,
		GitHubID:        31,
		IssueID:         pullIssue.ID,
		Issue:           pullIssue,
		Body:            "comment",
		Author:          author,
		HTMLURL:         "https://github.com/acme/widgets/issues/7#issuecomment-31",
		GitHubUpdatedAt: now,
	})
	require.True(t, ok)
	require.Equal(t, DocumentTypeIssueComment, commentDoc.DocumentType)

	reviewDoc, ok := buildPullRequestReviewDocument(database.PullRequestReview{
		RepositoryID:    repo.ID,
		GitHubID:        41,
		PullRequestID:   pullIssue.ID,
		PullRequest:     database.PullRequest{Issue: pullIssue, Number: pullIssue.Number, State: pullIssue.State},
		Body:            "review",
		State:           "APPROVED",
		Author:          author,
		HTMLURL:         "https://github.com/acme/widgets/pull/8#pullrequestreview-41",
		GitHubUpdatedAt: now,
	})
	require.True(t, ok)
	require.Equal(t, DocumentTypePullRequestReview, reviewDoc.DocumentType)

	reviewCommentDoc, ok := buildPullRequestReviewCommentDocument(database.PullRequestReviewComment{
		RepositoryID:    repo.ID,
		GitHubID:        51,
		PullRequestID:   pullIssue.ID,
		PullRequest:     database.PullRequest{Issue: pullIssue, Number: pullIssue.Number, State: pullIssue.State},
		Path:            "main.go",
		Body:            "nit",
		Author:          author,
		HTMLURL:         "https://github.com/acme/widgets/pull/8#discussion_r51",
		GitHubUpdatedAt: now,
	})
	require.True(t, ok)
	require.Equal(t, DocumentTypePullRequestReviewComment, reviewCommentDoc.DocumentType)

	require.Equal(t, "octocat", userLogin(author))
	re := regexp.MustCompile(`watchdog`)
	field, excerpt := bestMatchField(issueDoc, MentionRequest{Query: "Issue body", Mode: ModeFTS}, re)
	require.Equal(t, "body", field)
	require.NotEmpty(t, excerpt)
	require.NotEmpty(t, buildExcerpt("The heartbeat watchdog retries ACP messages", MentionRequest{Query: "watchdog", Mode: ModeRegex}, re))
}

type assertErr struct{}

func (assertErr) Error() string { return "boom" }

func seedSearchIndexCoverageData(t *testing.T, db *gorm.DB) (database.Repository, database.Issue, database.PullRequest, database.IssueComment, database.PullRequestReview, database.PullRequestReviewComment) {
	t.Helper()

	now := time.Now().UTC()
	author := database.User{GitHubID: 1, Login: "octocat", Type: "User"}
	require.NoError(t, db.Create(&author).Error)

	repo := database.Repository{
		ID:            1,
		GitHubID:      101,
		OwnerLogin:    "acme",
		Name:          "widgets",
		FullName:      "acme/widgets",
		DefaultBranch: "main",
	}
	require.NoError(t, db.Create(&repo).Error)

	issue := database.Issue{
		ID:              1,
		RepositoryID:    repo.ID,
		GitHubID:        201,
		Number:          7,
		Title:           "Issue title",
		Body:            "Issue body",
		State:           "open",
		AuthorID:        &author.ID,
		Author:          &author,
		HTMLURL:         "https://github.com/acme/widgets/issues/7",
		GitHubCreatedAt: now,
		GitHubUpdatedAt: now,
	}
	require.NoError(t, db.Create(&issue).Error)

	pullIssue := database.Issue{
		ID:                2,
		RepositoryID:      repo.ID,
		GitHubID:          202,
		Number:            8,
		Title:             "Pull title",
		Body:              "Pull body",
		State:             "open",
		AuthorID:          &author.ID,
		Author:            &author,
		IsPullRequest:     true,
		PullRequestAPIURL: "https://api.github.test/repos/acme/widgets/pulls/8",
		HTMLURL:           "https://github.com/acme/widgets/pull/8",
		GitHubCreatedAt:   now,
		GitHubUpdatedAt:   now,
	}
	require.NoError(t, db.Create(&pullIssue).Error)

	pull := database.PullRequest{
		IssueID:         pullIssue.ID,
		RepositoryID:    repo.ID,
		GitHubID:        301,
		Number:          8,
		State:           "open",
		HeadRef:         "feature",
		BaseRef:         "main",
		Issue:           pullIssue,
		HTMLURL:         "https://github.com/acme/widgets/pull/8",
		GitHubCreatedAt: now,
		GitHubUpdatedAt: now,
	}
	require.NoError(t, db.Create(&pull).Error)

	comment := database.IssueComment{
		GitHubID:        401,
		RepositoryID:    repo.ID,
		IssueID:         issue.ID,
		Issue:           issue,
		AuthorID:        &author.ID,
		Author:          &author,
		Body:            "comment",
		HTMLURL:         "https://github.com/acme/widgets/issues/7#issuecomment-401",
		GitHubCreatedAt: now,
		GitHubUpdatedAt: now,
	}
	require.NoError(t, db.Create(&comment).Error)

	review := database.PullRequestReview{
		GitHubID:        501,
		RepositoryID:    repo.ID,
		PullRequestID:   pull.IssueID,
		PullRequest:     pull,
		AuthorID:        &author.ID,
		Author:          &author,
		Body:            "review",
		State:           "APPROVED",
		HTMLURL:         "https://github.com/acme/widgets/pull/8#pullrequestreview-501",
		GitHubCreatedAt: now,
		GitHubUpdatedAt: now,
	}
	require.NoError(t, db.Create(&review).Error)

	reviewComment := database.PullRequestReviewComment{
		GitHubID:        601,
		RepositoryID:    repo.ID,
		PullRequestID:   pull.IssueID,
		PullRequest:     pull,
		AuthorID:        &author.ID,
		Author:          &author,
		Path:            "main.go",
		Body:            "nit",
		HTMLURL:         "https://github.com/acme/widgets/pull/8#discussion_r601",
		GitHubCreatedAt: now,
		GitHubUpdatedAt: now,
	}
	require.NoError(t, db.Create(&reviewComment).Error)

	return repo, issue, pull, comment, review, reviewComment
}
