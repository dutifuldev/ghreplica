package gitindex_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/dutifuldev/ghreplica/internal/github"
	"github.com/dutifuldev/ghreplica/internal/gitindex"
	"github.com/dutifuldev/ghreplica/internal/testfixtures"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestIndexPullRequestBuildsSnapshotAndCommitIndexes(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open("sqlite://file::memory:?cache=shared")
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	fixture := testfixtures.CreateLocalPullRepo(t)
	repo, pull := seedRepositoryAndPullRequest(t, db, fixture, 101)

	indexer := gitindex.NewService(db, github.NewClient("https://api.github.com", github.AuthConfig{}), filepath.Join(t.TempDir(), "mirrors"))
	require.NoError(t, indexer.IndexPullRequest(ctx, "acme", "widgets", repo, pull))

	var snapshot database.PullRequestChangeSnapshot
	require.NoError(t, db.WithContext(ctx).Where("repository_id = ? AND pull_request_number = ?", repo.ID, 101).First(&snapshot).Error)
	require.Equal(t, pull.HeadSHA, snapshot.HeadSHA)
	require.Equal(t, pull.BaseSHA, snapshot.BaseSHA)
	require.Equal(t, "main", snapshot.BaseRef)
	require.Equal(t, "full", snapshot.IndexedAs)
	require.Equal(t, "current", snapshot.IndexFreshness)
	require.GreaterOrEqual(t, snapshot.PathCount, 2)
	require.GreaterOrEqual(t, snapshot.HunkCount, 1)

	var refs []database.GitRef
	require.NoError(t, db.WithContext(ctx).Where("repository_id = ?", repo.ID).Order("ref_name ASC").Find(&refs).Error)
	refNames := make([]string, 0, len(refs))
	for _, ref := range refs {
		refNames = append(refNames, ref.RefName)
	}
	require.Contains(t, refNames, "refs/pull/101/head")
	require.Contains(t, refNames, "refs/remotes/origin/main")

	var files []database.PullRequestChangeFile
	require.NoError(t, db.WithContext(ctx).Where("snapshot_id = ?", snapshot.ID).Order("path ASC").Find(&files).Error)
	require.Len(t, files, 2)
	require.Equal(t, "app/service.go", files[0].Path)
	require.Equal(t, "modified", files[0].Status)
	require.NotEmpty(t, files[0].PatchText)
	require.Equal(t, "pkg/alpha.txt", files[1].Path)

	var hunks []database.PullRequestChangeHunk
	require.NoError(t, db.WithContext(ctx).Where("snapshot_id = ?", snapshot.ID).Order("path ASC, hunk_index ASC").Find(&hunks).Error)
	require.NotEmpty(t, hunks)

	var commit database.GitCommit
	require.NoError(t, db.WithContext(ctx).Where("repository_id = ? AND sha = ?", repo.ID, pull.HeadSHA).First(&commit).Error)

	var commitParents []database.GitCommitParent
	require.NoError(t, db.WithContext(ctx).Where("repository_id = ? AND commit_sha = ?", repo.ID, pull.HeadSHA).Find(&commitParents).Error)
	require.Len(t, commitParents, 1)

	var commitFiles []database.GitCommitParentFile
	require.NoError(t, db.WithContext(ctx).Where("repository_id = ? AND commit_sha = ?", repo.ID, pull.HeadSHA).Order("path ASC").Find(&commitFiles).Error)
	require.Len(t, commitFiles, 2)
	require.Equal(t, "app/service.go", commitFiles[0].Path)

	var commitHunks []database.GitCommitParentHunk
	require.NoError(t, db.WithContext(ctx).Where("repository_id = ? AND commit_sha = ?", repo.ID, pull.HeadSHA).Find(&commitHunks).Error)
	require.NotEmpty(t, commitHunks)
}

func seedRepositoryAndPullRequest(t *testing.T, db *gorm.DB, fixture testfixtures.LocalPullRepo, number int) (database.Repository, database.PullRequest) {
	t.Helper()

	repo := database.Repository{
		GitHubID:      101,
		OwnerLogin:    "acme",
		Name:          "widgets",
		FullName:      "acme/widgets",
		HTMLURL:       fixture.RemoteURL,
		APIURL:        "https://api.github.com/repos/acme/widgets",
		DefaultBranch: "main",
		Visibility:    "public",
	}
	require.NoError(t, db.Create(&repo).Error)

	issue := database.Issue{
		RepositoryID:  repo.ID,
		GitHubID:      int64(1000 + number),
		Number:        number,
		Title:         fmt.Sprintf("PR %d", number),
		State:         "open",
		IsPullRequest: true,
		HTMLURL:       fmt.Sprintf("https://github.com/acme/widgets/pull/%d", number),
		APIURL:        fmt.Sprintf("https://api.github.com/repos/acme/widgets/issues/%d", number),
	}
	require.NoError(t, db.Create(&issue).Error)

	ref := fixture.Pulls[number]
	pull := database.PullRequest{
		IssueID:      issue.ID,
		RepositoryID: repo.ID,
		GitHubID:     int64(2000 + number),
		Number:       number,
		State:        "open",
		HeadRef:      ref.HeadRef,
		HeadSHA:      ref.HeadSHA,
		BaseRef:      "main",
		BaseSHA:      fixture.BaseSHA,
		HTMLURL:      fmt.Sprintf("https://github.com/acme/widgets/pull/%d", number),
		APIURL:       fmt.Sprintf("https://api.github.com/repos/acme/widgets/pulls/%d", number),
	}
	require.NoError(t, db.Create(&pull).Error)

	return repo, pull
}
