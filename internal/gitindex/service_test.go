package gitindex_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestIndexPullRequestReusesExistingCommitDetail(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open("sqlite://" + filepath.Join(t.TempDir(), "reuse-commit-detail.db"))
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	fixture := testfixtures.CreateLocalPullRepo(t)
	repo, pull := seedRepositoryAndPullRequest(t, db, fixture, 101)

	indexer := gitindex.NewService(db, github.NewClient("https://api.github.com", github.AuthConfig{}), filepath.Join(t.TempDir(), "mirrors"))
	require.NoError(t, indexer.IndexPullRequest(ctx, "acme", "widgets", repo, pull))

	var parent database.GitCommitParent
	require.NoError(t, db.WithContext(ctx).
		Where("repository_id = ? AND commit_sha = ?", repo.ID, pull.HeadSHA).
		First(&parent).Error)

	sentinel := time.Date(2026, time.April, 18, 15, 0, 0, 0, time.UTC)
	require.NoError(t, db.WithContext(ctx).
		Model(&database.GitCommitParent{}).
		Where("id = ?", parent.ID).
		Update("updated_at", sentinel).Error)

	require.NoError(t, indexer.IndexPullRequest(ctx, "acme", "widgets", repo, pull))

	var stored database.GitCommitParent
	require.NoError(t, db.WithContext(ctx).
		Where("repository_id = ? AND commit_sha = ? AND parent_index = ?", repo.ID, pull.HeadSHA, parent.ParentIndex).
		First(&stored).Error)
	require.Equal(t, sentinel, stored.UpdatedAt.UTC())
}

func TestIndexPullRequestSkipsOversizedMergeCommitDetail(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open("sqlite://file::memory:?cache=shared")
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	fixture := createMergeHeavyPullRepo(t)
	repo, pull := seedRepositoryAndPullRequest(t, db, fixture, 201)

	indexer := gitindex.NewService(db, github.NewClient("https://api.github.com", github.AuthConfig{}), filepath.Join(t.TempDir(), "mirrors"))
	require.NoError(t, indexer.IndexPullRequest(ctx, "acme", "widgets", repo, pull))

	var snapshot database.PullRequestChangeSnapshot
	require.NoError(t, db.WithContext(ctx).Where("repository_id = ? AND pull_request_number = ?", repo.ID, 201).First(&snapshot).Error)
	require.Equal(t, "full", snapshot.IndexedAs)
	require.Equal(t, "current", snapshot.IndexFreshness)

	var mergeCommit database.GitCommit
	require.NoError(t, db.WithContext(ctx).Where("repository_id = ? AND sha = ?", repo.ID, pull.HeadSHA).First(&mergeCommit).Error)

	var commitParents []database.GitCommitParent
	require.NoError(t, db.WithContext(ctx).Where("repository_id = ? AND commit_sha = ?", repo.ID, pull.HeadSHA).Order("parent_index ASC").Find(&commitParents).Error)
	require.Len(t, commitParents, 2)

	var skippedParents []database.GitCommitParent
	for _, parent := range commitParents {
		if parent.IndexedAs == "skipped" {
			skippedParents = append(skippedParents, parent)
		}
	}
	require.Len(t, skippedParents, 1)
	require.Equal(t, "oversized_merge_commit", skippedParents[0].IndexReason)
	require.Greater(t, skippedParents[0].PathCount, 150)

	var skippedFiles []database.GitCommitParentFile
	require.NoError(t, db.WithContext(ctx).
		Where("repository_id = ? AND commit_sha = ? AND parent_index = ?", repo.ID, pull.HeadSHA, skippedParents[0].ParentIndex).
		Find(&skippedFiles).Error)
	require.Empty(t, skippedFiles)

	var indexedFiles []database.GitCommitParentFile
	require.NoError(t, db.WithContext(ctx).
		Where("repository_id = ? AND commit_sha = ?", repo.ID, pull.HeadSHA).
		Find(&indexedFiles).Error)
	require.NotEmpty(t, indexedFiles)
}

func TestIndexPullRequestMarksPathOnlyMergeCommitFilesAsPathOnly(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open("sqlite://file::memory:?cache=shared")
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	fixture := createMergePathOnlyPullRepo(t)
	repo, pull := seedRepositoryAndPullRequest(t, db, fixture, 202)

	indexer := gitindex.NewService(db, github.NewClient("https://api.github.com", github.AuthConfig{}), filepath.Join(t.TempDir(), "mirrors"))
	require.NoError(t, indexer.IndexPullRequest(ctx, "acme", "widgets", repo, pull))

	var pathOnlyParent database.GitCommitParent
	require.NoError(t, db.WithContext(ctx).
		Where("repository_id = ? AND commit_sha = ? AND indexed_as = ?", repo.ID, pull.HeadSHA, "paths_only").
		First(&pathOnlyParent).Error)
	require.Equal(t, "oversized_merge_commit", pathOnlyParent.IndexReason)

	var fileRows []database.GitCommitParentFile
	require.NoError(t, db.WithContext(ctx).
		Where("repository_id = ? AND commit_sha = ? AND parent_index = ?", repo.ID, pull.HeadSHA, pathOnlyParent.ParentIndex).
		Find(&fileRows).Error)
	require.NotEmpty(t, fileRows)
	for _, row := range fileRows {
		require.Equal(t, "paths_only", row.IndexedAs)
		require.Empty(t, row.PatchText)
	}
}

func seedRepositoryAndPullRequest(t *testing.T, db *gorm.DB, fixture testfixtures.LocalPullRepo, number int) (database.Repository, database.PullRequest) {
	t.Helper()

	repo := database.Repository{
		GitHubID:      int64(10_000 + number),
		OwnerLogin:    "acme",
		Name:          fmt.Sprintf("widgets-%d", number),
		FullName:      fmt.Sprintf("acme/widgets-%d", number),
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

func createMergeHeavyPullRepo(t *testing.T) testfixtures.LocalPullRepo {
	t.Helper()

	root := t.TempDir()
	remotePath := filepath.Join(root, "remote.git")
	runGitCommand(t, "", "git", "init", "--bare", remotePath)

	worktree := filepath.Join(root, "worktree")
	runGitCommand(t, "", "git", "clone", remotePath, worktree)
	runGitCommand(t, worktree, "git", "config", "user.name", "Test User")
	runGitCommand(t, worktree, "git", "config", "user.email", "test@example.com")
	runGitCommand(t, worktree, "git", "checkout", "-b", "main")

	writeTestFile(t, filepath.Join(worktree, "app", "service.go"), "package app\n\nfunc run() {\n\tstepOne()\n}\n")
	runGitCommand(t, worktree, "git", "add", ".")
	runGitCommand(t, worktree, "git", "commit", "-m", "initial")
	runGitCommand(t, worktree, "git", "push", "origin", "HEAD:refs/heads/main")

	runGitCommand(t, worktree, "git", "checkout", "-B", "feature-merge-heavy", "main")
	writeTestFile(t, filepath.Join(worktree, "app", "service.go"), "package app\n\nfunc run() {\n\tstepOne()\n\tfeatureOnly()\n}\n")
	runGitCommand(t, worktree, "git", "add", "app/service.go")
	runGitCommand(t, worktree, "git", "commit", "-m", "feature change")

	runGitCommand(t, worktree, "git", "checkout", "main")
	for i := 0; i < 200; i++ {
		writeTestFile(t, filepath.Join(worktree, "bulk", fmt.Sprintf("file-%03d.txt", i)), fmt.Sprintf("main-%03d\n", i))
	}
	runGitCommand(t, worktree, "git", "add", ".")
	runGitCommand(t, worktree, "git", "commit", "-m", "bulk main change")
	baseSHA := strings.TrimSpace(runGitCommand(t, worktree, "git", "rev-parse", "HEAD"))
	runGitCommand(t, worktree, "git", "push", "origin", "HEAD:refs/heads/main")

	runGitCommand(t, worktree, "git", "checkout", "feature-merge-heavy")
	runGitCommand(t, worktree, "git", "merge", "--no-ff", "main", "-m", "merge main into feature")
	headSHA := strings.TrimSpace(runGitCommand(t, worktree, "git", "rev-parse", "HEAD"))
	runGitCommand(t, worktree, "git", "push", "--force", "origin", "HEAD:refs/heads/feature-merge-heavy")
	runGitCommand(t, worktree, "git", "push", "--force", "origin", "HEAD:refs/pull/201/head")

	return testfixtures.LocalPullRepo{
		RemoteURL: "file://" + remotePath,
		BaseSHA:   baseSHA,
		Pulls: map[int]testfixtures.LocalPullRef{
			201: {
				Number:  201,
				HeadRef: "feature-merge-heavy",
				HeadSHA: headSHA,
			},
		},
	}
}

func createMergePathOnlyPullRepo(t *testing.T) testfixtures.LocalPullRepo {
	t.Helper()

	root := t.TempDir()
	remotePath := filepath.Join(root, "remote.git")
	runGitCommand(t, "", "git", "init", "--bare", remotePath)

	worktree := filepath.Join(root, "worktree")
	runGitCommand(t, "", "git", "clone", remotePath, worktree)
	runGitCommand(t, worktree, "git", "config", "user.name", "Test User")
	runGitCommand(t, worktree, "git", "config", "user.email", "test@example.com")
	runGitCommand(t, worktree, "git", "checkout", "-b", "main")

	writeTestFile(t, filepath.Join(worktree, "app", "service.go"), "package app\n\nfunc run() {\n\tstepOne()\n}\n")
	runGitCommand(t, worktree, "git", "add", ".")
	runGitCommand(t, worktree, "git", "commit", "-m", "initial")
	runGitCommand(t, worktree, "git", "push", "origin", "HEAD:refs/heads/main")

	runGitCommand(t, worktree, "git", "checkout", "-B", "feature-merge-path-only", "main")
	writeTestFile(t, filepath.Join(worktree, "app", "service.go"), "package app\n\nfunc run() {\n\tstepOne()\n\tfeatureOnly()\n}\n")
	runGitCommand(t, worktree, "git", "add", "app/service.go")
	runGitCommand(t, worktree, "git", "commit", "-m", "feature change")

	runGitCommand(t, worktree, "git", "checkout", "main")
	for i := 0; i < 50; i++ {
		writeTestFile(t, filepath.Join(worktree, "bulk", fmt.Sprintf("file-%03d.txt", i)), fmt.Sprintf("main-%03d\n", i))
	}
	runGitCommand(t, worktree, "git", "add", ".")
	runGitCommand(t, worktree, "git", "commit", "-m", "bulk main change")
	baseSHA := strings.TrimSpace(runGitCommand(t, worktree, "git", "rev-parse", "HEAD"))
	runGitCommand(t, worktree, "git", "push", "origin", "HEAD:refs/heads/main")

	runGitCommand(t, worktree, "git", "checkout", "feature-merge-path-only")
	runGitCommand(t, worktree, "git", "merge", "--no-ff", "main", "-m", "merge main into feature")
	headSHA := strings.TrimSpace(runGitCommand(t, worktree, "git", "rev-parse", "HEAD"))
	runGitCommand(t, worktree, "git", "push", "--force", "origin", "HEAD:refs/heads/feature-merge-path-only")
	runGitCommand(t, worktree, "git", "push", "--force", "origin", "HEAD:refs/pull/202/head")

	return testfixtures.LocalPullRepo{
		RemoteURL: "file://" + remotePath,
		BaseSHA:   baseSHA,
		Pulls: map[int]testfixtures.LocalPullRef{
			202: {
				Number:  202,
				HeadRef: "feature-merge-path-only",
				HeadSHA: headSHA,
			},
		},
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func runGitCommand(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()

	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "%s %s failed: %s", name, strings.Join(args, " "), string(out))
	return string(out)
}
