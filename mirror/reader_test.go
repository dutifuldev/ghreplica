package mirror

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestReaderLooksUpRepositoriesIssuesAndPullRequests(t *testing.T) {
	db := openTestDB(t)
	reader := NewReader(db)
	ctx := context.Background()

	author := User{GitHubID: 501, Login: "alice"}
	require.NoError(t, db.Create(&author).Error)
	repository := Repository{GitHubID: 101, OwnerID: &author.ID, OwnerLogin: "acme", Name: "widgets", FullName: "acme/widgets"}
	require.NoError(t, db.Create(&repository).Error)
	now := time.Now().UTC().Truncate(time.Second)
	issue := Issue{
		RepositoryID:    repository.ID,
		GitHubID:        1101,
		Number:          11,
		Title:           "issue title",
		State:           "open",
		AuthorID:        &author.ID,
		HTMLURL:         "https://github.com/acme/widgets/issues/11",
		GitHubUpdatedAt: now,
	}
	require.NoError(t, db.Create(&issue).Error)
	pullIssue := Issue{
		RepositoryID:    repository.ID,
		GitHubID:        2202,
		Number:          22,
		Title:           "pull title",
		State:           "open",
		AuthorID:        &author.ID,
		HTMLURL:         "https://github.com/acme/widgets/pull/22",
		GitHubUpdatedAt: now,
	}
	require.NoError(t, db.Create(&pullIssue).Error)
	pull := PullRequest{
		IssueID:         pullIssue.ID,
		RepositoryID:    repository.ID,
		GitHubID:        2202,
		Number:          22,
		State:           "open",
		HTMLURL:         pullIssue.HTMLURL,
		GitHubUpdatedAt: now,
	}
	require.NoError(t, db.Create(&pull).Error)

	byName, err := reader.RepositoryByOwnerName(ctx, "acme", "widgets")
	require.NoError(t, err)
	require.Equal(t, repository.GitHubID, byName.GitHubID)
	require.Equal(t, "alice", byName.Owner.Login)

	byID, err := reader.RepositoryByGitHubID(ctx, 101)
	require.NoError(t, err)
	require.Equal(t, "acme/widgets", byID.FullName)

	readIssue, err := reader.IssueByGitHubRepositoryID(ctx, 101, 11)
	require.NoError(t, err)
	require.Equal(t, "issue title", readIssue.Title)
	require.Equal(t, "alice", readIssue.Author.Login)

	readPull, err := reader.PullRequestByGitHubRepositoryID(ctx, 101, 22)
	require.NoError(t, err)
	require.Equal(t, "pull title", readPull.Issue.Title)
	require.Equal(t, "alice", readPull.Issue.Author.Login)

	issues, err := reader.IssuesByGitHubRepositoryID(ctx, 101, []int{11})
	require.NoError(t, err)
	require.Len(t, issues, 1)

	pulls, err := reader.PullRequestsByGitHubRepositoryID(ctx, 101, []int{22})
	require.NoError(t, err)
	require.Len(t, pulls, 1)
}

func TestReaderReturnsEmptySlicesForEmptyNumberLists(t *testing.T) {
	db := openTestDB(t)
	reader := NewReader(db)
	ctx := context.Background()

	issues, err := reader.IssuesByRepositoryID(ctx, 1, nil)
	require.NoError(t, err)
	require.Empty(t, issues)

	pulls, err := reader.PullRequestsByRepositoryID(ctx, 1, nil)
	require.NoError(t, err)
	require.Empty(t, pulls)
}

func TestObjectConversions(t *testing.T) {
	author := User{Login: "alice"}
	repository := Repository{GitHubID: 101, OwnerLogin: "acme", Name: "widgets", FullName: "acme/widgets", Owner: &author}
	repositoryObject := RepositoryObjectFromRow(repository)
	require.Equal(t, int64(101), repositoryObject.ID)
	require.Equal(t, "alice", repositoryObject.Owner.Login)

	now := time.Now().UTC()
	issue := Issue{GitHubID: 11, Number: 11, Title: "issue", State: "open", HTMLURL: "issue-url", GitHubUpdatedAt: now, Author: &author}
	require.Equal(t, "alice", IssueObjectFromRow(issue).User.Login)
	require.Equal(t, "issue", SummaryFromIssue(issue).Title)

	pull := PullRequest{GitHubID: 22, Number: 22, State: "open", HTMLURL: "pull-url", GitHubUpdatedAt: now, Issue: Issue{Title: "pull", Author: &author}}
	require.Equal(t, "alice", PullRequestObjectFromRow(pull).User.Login)
	require.Equal(t, "pull", SummaryFromPullRequest(pull).Title)
}

func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&User{}, &Repository{}, &Issue{}, &PullRequest{}))
	return db
}
