package gitindex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/dutifuldev/ghreplica/internal/github"
	"github.com/dutifuldev/ghreplica/internal/testfixtures"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestIndexPullRequestTimesOutWaitingForRepoLock(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open("sqlite://" + filepath.Join(t.TempDir(), "gitindex-timeout.db"))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	fixture := testfixtures.CreateLocalPullRepo(t)
	repo, pull := seedGitIndexRepositoryAndPullRequest(t, db, fixture, 101)

	indexer := NewService(db, github.NewClient("https://api.github.com", github.AuthConfig{}), filepath.Join(t.TempDir(), "mirrors"))
	indexer.indexTimeout = 100 * time.Millisecond

	lockPath := indexer.lockPath("acme", "widgets")
	require.NoError(t, os.MkdirAll(filepath.Dir(lockPath), 0o755))
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	require.NoError(t, err)
	defer func() { require.NoError(t, lockFile.Close()) }()
	require.NoError(t, syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB))
	defer func() { require.NoError(t, syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)) }()

	start := time.Now()
	err = indexer.IndexPullRequest(ctx, "acme", "widgets", repo, pull)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(start), time.Second)
}

func seedGitIndexRepositoryAndPullRequest(t *testing.T, db *gorm.DB, fixture testfixtures.LocalPullRepo, number int) (database.Repository, database.PullRequest) {
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
