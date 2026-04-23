package gitindex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	gh "github.com/dutifuldev/ghreplica/internal/github"
	"github.com/dutifuldev/ghreplica/internal/testfixtures"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestStructuralSearchHelpers(t *testing.T) {
	t.Parallel()

	request, err := normalizeStructuralSearchRequest(StructuralSearchRequest{
		CommitSHA: "  deadbeef  ",
		Language:  " go ",
		Rule:      map[string]any{"pattern": "run()"},
		Paths: []string{
			"./app/service.go",
			"app\\service.go",
			"../ignored",
			"",
		},
	})
	require.NoError(t, err)
	require.Equal(t, "deadbeef", request.CommitSHA)
	require.Equal(t, "go", request.Language)
	require.Equal(t, []string{"app/service.go"}, request.Paths)
	require.Equal(t, 100, request.Limit)

	_, err = normalizeStructuralSearchRequest(StructuralSearchRequest{
		CommitSHA: "deadbeef",
		Ref:       "main",
		Language:  "go",
		Rule:      map[string]any{"pattern": "run()"},
	})
	require.ErrorIs(t, err, ErrInvalidStructuralSearchRequest)

	_, err = normalizeStructuralSearchRequest(StructuralSearchRequest{
		PullRequestNumber: 101,
		ChangedFilesOnly:  true,
		Rule:              map[string]any{"pattern": "run()"},
	})
	require.ErrorIs(t, err, ErrInvalidStructuralSearchRequest)

	ref, err := normalizeResolvedRef("refs/remotes/origin/main")
	require.NoError(t, err)
	require.Equal(t, "refs/heads/main", ref)

	ref, err = normalizeResolvedRef("feature/topic")
	require.NoError(t, err)
	require.Equal(t, "refs/heads/feature/topic", ref)

	_, err = normalizeResolvedRef("refs/tags/v1")
	require.ErrorIs(t, err, ErrInvalidStructuralSearchRequest)

	require.Equal(t, []string{"app/service.go"}, intersectPaths(
		[]string{"app/service.go", "docs/readme.md"},
		[]string{"./app/service.go", "missing.txt"},
	))
	require.Equal(t, map[string]string{"FUNC": "run"}, extractSingleMetaVariables(map[string]astGrepJSONNode{
		"FUNC": {Text: "run"},
	}))
	require.Equal(t, map[string][]string{"ARGS": []string{"one", "two"}}, extractMultiMetaVariables(map[string][]astGrepJSONNode{
		"ARGS": {{Text: "one"}, {Text: "two"}},
	}))
	require.True(t, looksLikeInvalidASTGrepRequest("unknown language: madeup"))
	require.False(t, looksLikeInvalidASTGrepRequest("permission denied"))
	require.True(t, IsInvalidStructuralSearchRequest(fmt.Errorf("%w: bad request", ErrInvalidStructuralSearchRequest)))
	require.True(t, IsStructuralSearchTargetNotFound(fmt.Errorf("%w: missing", ErrStructuralSearchTargetNotFound)))
}

func TestResolveGitRefOrSHAUsesStoredGitRef(t *testing.T) {
	t.Parallel()

	db, err := database.Open("sqlite://file::memory:?cache=shared")
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	service := NewService(db, gh.NewClient("https://api.github.com", gh.AuthConfig{}), t.TempDir())

	require.NoError(t, db.Create(&database.GitRef{
		RepositoryID:    7,
		RefName:         "refs/heads/main",
		TargetOID:       "target-sha",
		PeeledCommitSHA: "peeled-sha",
	}).Error)

	sha, err := service.resolveGitRefOrSHA(context.Background(), 7, "main")
	require.NoError(t, err)
	require.Equal(t, "peeled-sha", sha)

	sha, err = service.resolveGitRefOrSHA(context.Background(), 7, "unknown")
	require.NoError(t, err)
	require.Equal(t, "unknown", sha)
}

func TestSearchStructuralPullRequestTarget(t *testing.T) {
	ctx := context.Background()
	db, service, fixture, pull := newStructuralSearchHarness(t)

	logPath := filepath.Join(t.TempDir(), "astgrep-pr.log")
	service.WithASTGrepBinary(writeFakeASTGrepBinary(t, logPath, `[{"text":"parseOne()","file":"./app/service.go","range":{"start":{"line":3,"column":1},"end":{"line":3,"column":11}},"metaVariables":{"single":{"FUNC":{"text":"parseOne"}},"multi":{"ARGS":[{"text":"x"},{"text":"y"}]},"transformed":{"UPPER":{"text":"PARSEONE"}}}},{"text":"parseTwo()","file":"./app/service.go","range":{"start":{"line":4,"column":1},"end":{"line":4,"column":11}},"metaVariables":{"single":{"FUNC":{"text":"parseTwo"}}}}]`, "", 0))

	response, err := service.SearchStructural(ctx, "acme", "widgets", StructuralSearchRequest{
		PullRequestNumber: pull.Number,
		Language:          "go",
		Rule:              map[string]any{"pattern": "parse($$ARGS)"},
		Paths:             []string{"./app/service.go", "pkg/missing.txt"},
		ChangedFilesOnly:  true,
		Limit:             1,
	})
	require.NoError(t, err)
	require.Equal(t, fixture.Pulls[pull.Number].HeadSHA, response.ResolvedCommitSHA)
	require.Equal(t, fmt.Sprintf("refs/pull/%d/head", pull.Number), response.ResolvedRef)
	require.True(t, response.Truncated)
	require.Len(t, response.Matches, 1)
	require.Equal(t, "app/service.go", response.Matches[0].Path)
	require.Equal(t, 4, response.Matches[0].StartLine)
	require.Equal(t, "parseOne", response.Matches[0].MetaVariables.Single["FUNC"])
	require.Equal(t, []string{"x", "y"}, response.Matches[0].MetaVariables.Multi["ARGS"])
	require.Equal(t, "PARSEONE", response.Matches[0].MetaVariables.Transformed["UPPER"])

	args := readLoggedArgs(t, logPath)
	require.Contains(t, args, "scan")
	require.Contains(t, args, "app/service.go")
	require.NotContains(t, args, "pkg/missing.txt")

	var refs []database.GitRef
	require.NoError(t, db.Where("repository_id = ?", 1).Find(&refs).Error)
	require.NotEmpty(t, refs)
}

func TestSearchStructuralRefAndCommitTargets(t *testing.T) {
	ctx := context.Background()
	_, service, fixture, _ := newStructuralSearchHarness(t)

	refLogPath := filepath.Join(t.TempDir(), "astgrep-ref.log")
	service.WithASTGrepBinary(writeFakeASTGrepBinary(t, refLogPath, `[{"text":"hello","file":"./docs/readme.md","range":{"start":{"line":0,"column":0},"end":{"line":0,"column":5}}}]`, "", 0))

	response, err := service.SearchStructural(ctx, "acme", "widgets", StructuralSearchRequest{
		Ref:      "main",
		Language: "go",
		Rule:     map[string]any{"pattern": "hello"},
		Paths:    []string{"./docs/readme.md", "missing.txt"},
		Limit:    5,
	})
	require.NoError(t, err)
	require.Equal(t, "refs/heads/main", response.ResolvedRef)
	require.Equal(t, fixture.BaseSHA, response.ResolvedCommitSHA)
	require.Len(t, response.Matches, 1)
	require.Equal(t, "docs/readme.md", response.Matches[0].Path)
	require.Contains(t, readLoggedArgs(t, refLogPath), "docs/readme.md")

	_, err = service.SearchStructural(ctx, "acme", "widgets", StructuralSearchRequest{
		CommitSHA: "deadbeef",
		Language:  "go",
		Rule:      map[string]any{"pattern": "run()"},
	})
	require.ErrorIs(t, err, ErrStructuralSearchTargetNotFound)
}

func TestRunASTGrepMarksInvalidRequests(t *testing.T) {
	t.Parallel()

	db, err := database.Open("sqlite://file::memory:?cache=shared")
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644))

	service := NewService(db, gh.NewClient("https://api.github.com", gh.AuthConfig{}), t.TempDir()).
		WithASTGrepBinary(writeFakeASTGrepBinary(t, "", "", "unknown language: nope", 1))

	_, _, err = service.runASTGrep(context.Background(), root, StructuralSearchRequest{
		Language: "nope",
		Rule:     map[string]any{"pattern": "main"},
		Limit:    1,
	}, nil)
	require.Error(t, err)
	require.True(t, IsInvalidStructuralSearchRequest(err))
}

func TestServiceOptionsAndMarkBaseRefStale(t *testing.T) {
	t.Parallel()

	db, err := database.Open("sqlite://file::memory:?cache=shared")
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	service := NewService(db, gh.NewClient("https://api.github.com", gh.AuthConfig{}), t.TempDir())
	require.Equal(t, defaultIndexTimeout, service.indexTimeout)
	require.Equal(t, defaultASTGrepTimeout, service.astGrepTimeout)

	require.Same(t, service, service.WithIndexTimeout(2*time.Minute))
	require.Equal(t, 2*time.Minute, service.indexTimeout)
	require.Same(t, service, service.WithASTGrepTimeout(10*time.Second))
	require.Equal(t, 10*time.Second, service.astGrepTimeout)
	require.Same(t, service, service.WithASTGrepBinary(" /tmp/ast-grep "))
	require.Equal(t, "/tmp/ast-grep", service.astGrepBin)

	snapshot := database.PullRequestChangeSnapshot{
		RepositoryID:      9,
		PullRequestID:     1,
		PullRequestNumber: 101,
		HeadSHA:           "head",
		BaseSHA:           "base",
		MergeBaseSHA:      "merge",
		BaseRef:           "main",
		State:             "open",
		IndexedAs:         indexedAsFull,
		IndexFreshness:    freshnessCurrent,
	}
	require.NoError(t, db.Create(&snapshot).Error)

	require.NoError(t, service.MarkBaseRefStale(context.Background(), 9, "main"))

	var stored database.PullRequestChangeSnapshot
	require.NoError(t, db.First(&stored, snapshot.ID).Error)
	require.Equal(t, freshnessStaleBaseMoved, stored.IndexFreshness)
}

func TestGitIndexFailureAndPathOnlyHelpers(t *testing.T) {
	t.Parallel()

	db, err := database.Open("sqlite://" + filepath.Join(t.TempDir(), "gitindex-helpers.db"))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	service := NewService(db, gh.NewClient("https://api.github.com", gh.AuthConfig{}), t.TempDir())

	pull := database.PullRequest{
		IssueID:      12,
		RepositoryID: 3,
		Number:       101,
		State:        "open",
		HeadSHA:      "head",
		BaseSHA:      "base",
		BaseRef:      "refs/heads/main",
	}
	require.NoError(t, service.markSnapshotFailed(context.Background(), 3, pull, "merge", errors.New("boom")))

	var snapshot database.PullRequestChangeSnapshot
	require.NoError(t, db.Where("repository_id = ? AND pull_request_number = ?", 3, 101).First(&snapshot).Error)
	require.Equal(t, indexedAsFailed, snapshot.IndexedAs)
	require.Equal(t, freshnessFailed, snapshot.IndexFreshness)
	require.Equal(t, "main", snapshot.BaseRef)

	files := map[string]*parsedFile{
		"app/service.go": {
			Path:      "app/service.go",
			IndexedAs: indexedAsFull,
			PatchText: "diff",
			Hunks:     []Hunk{{Index: 1, DiffHunk: "@@"}},
		},
	}
	downgradeCommitFilesToPathsOnly(files)
	require.Equal(t, indexedAsPathOnly, files["app/service.go"].IndexedAs)
	require.Empty(t, files["app/service.go"].PatchText)
	require.Nil(t, files["app/service.go"].Hunks)

	require.Equal(t, "AUTHORIZATION: basic eC1hY2Nlc3MtdG9rZW46dG9rZW4=", basicAuthHeader("token"))
	require.Equal(t, "file:///tmp/repo.git", repositoryGitURL("file:///tmp/repo.git"))
	require.Equal(t, "https://github.com/acme/widgets.git", repositoryGitURL("https://github.com/acme/widgets"))
	require.Equal(t, "binary", classifyFileKind(rawRecord{}, true, 0, 0))
	require.Equal(t, "added", normalizeStatus("A"))
}

func newStructuralSearchHarness(t *testing.T) (*gorm.DB, *Service, testfixtures.LocalPullRepo, database.PullRequest) {
	t.Helper()

	db, err := database.Open("sqlite://" + filepath.Join(t.TempDir(), "gitindex-test.db"))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	fixture := testfixtures.CreateLocalPullRepo(t)
	repo := database.Repository{
		ID:            1,
		GitHubID:      1001,
		OwnerLogin:    "acme",
		Name:          "widgets",
		FullName:      "acme/widgets",
		HTMLURL:       fixture.RemoteURL,
		APIURL:        "https://api.github.com/repos/acme/widgets",
		DefaultBranch: "main",
		Visibility:    "public",
	}
	require.NoError(t, db.Create(&repo).Error)

	pullRef := fixture.Pulls[101]
	issue := database.Issue{
		ID:            1,
		RepositoryID:  repo.ID,
		GitHubID:      2001,
		Number:        101,
		Title:         "Search me",
		State:         "open",
		IsPullRequest: true,
		HTMLURL:       "https://github.com/acme/widgets/pull/101",
		APIURL:        "https://api.github.com/repos/acme/widgets/issues/101",
	}
	require.NoError(t, db.Create(&issue).Error)

	pull := database.PullRequest{
		IssueID:         issue.ID,
		RepositoryID:    repo.ID,
		GitHubID:        3001,
		Number:          101,
		State:           "open",
		HeadRef:         pullRef.HeadRef,
		HeadSHA:         pullRef.HeadSHA,
		BaseRef:         "main",
		BaseSHA:         fixture.BaseSHA,
		HTMLURL:         "https://github.com/acme/widgets/pull/101",
		APIURL:          "https://api.github.com/repos/acme/widgets/pulls/101",
		GitHubCreatedAt: time.Now().UTC(),
		GitHubUpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, db.Create(&pull).Error)

	service := NewService(db, gh.NewClient("https://api.github.com", gh.AuthConfig{}), filepath.Join(t.TempDir(), "mirrors"))
	return db, service, fixture, pull
}

func writeFakeASTGrepBinary(t *testing.T, logPath, stdout, stderr string, exitCode int) string {
	t.Helper()

	scriptPath := filepath.Join(t.TempDir(), "ast-grep")
	var body strings.Builder
	body.WriteString("#!/bin/sh\nset -eu\n")
	if logPath != "" {
		_, _ = fmt.Fprintf(&body, "printf '%%s\\n' \"$@\" > %q\n", logPath)
	}
	if stderr != "" {
		_, _ = fmt.Fprintf(&body, "printf '%%s\\n' %q >&2\n", stderr)
	}
	if stdout != "" {
		body.WriteString("cat <<'EOF'\n")
		body.WriteString(stdout)
		body.WriteString("\nEOF\n")
	}
	if exitCode != 0 {
		_, _ = fmt.Fprintf(&body, "exit %d\n", exitCode)
	}
	require.NoError(t, os.WriteFile(scriptPath, []byte(body.String()), 0o755))
	return scriptPath
}

func readLoggedArgs(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return strings.Fields(string(raw))
}

func TestFileClassificationHelpers(t *testing.T) {
	t.Parallel()

	require.Equal(t, "renamed", normalizeStatus("R100"))
	require.Equal(t, "copied", normalizeStatus("C090"))
	require.Equal(t, "added", normalizeStatus("A"))
	require.Equal(t, "removed", normalizeStatus("D"))
	require.Equal(t, "type_changed", normalizeStatus("T"))
	require.Equal(t, "modified", normalizeStatus("M"))

	require.Equal(t, "submodule", classifyFileKind(rawRecord{OldMode: "160000"}, false, 0, 0))
	require.Equal(t, "symlink", classifyFileKind(rawRecord{NewMode: "120000"}, false, 0, 0))
	require.Equal(t, "binary", classifyFileKind(rawRecord{}, true, 0, 0))
	require.Equal(t, "mode_only", classifyFileKind(rawRecord{Status: "T", OldMode: "100644", NewMode: "100755"}, false, 0, 0))
	require.Equal(t, "text", classifyFileKind(rawRecord{Status: "M", OldMode: "100644", NewMode: "100644"}, false, 1, 1))
}

func TestSyncResolvedRefAndParseNumstatZ(t *testing.T) {
	t.Parallel()

	db, err := database.Open("sqlite://file::memory:?cache=shared")
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	service := NewService(db, gh.NewClient("https://api.github.com", gh.AuthConfig{}), t.TempDir())
	service.gitBin = writeFakeGitBinary(t)
	require.NoError(t, service.syncResolvedRef(context.Background(), 1, t.TempDir(), "refs/pull/17/head"))
	require.NoError(t, service.syncResolvedRef(context.Background(), 1, t.TempDir(), "refs/heads/main"))
	require.ErrorIs(t, service.syncResolvedRef(context.Background(), 1, t.TempDir(), "refs/tags/v1"), ErrInvalidStructuralSearchRequest)
	require.ErrorIs(t, service.syncResolvedRef(context.Background(), 1, t.TempDir(), "refs/pull/nope/head"), ErrInvalidStructuralSearchRequest)

	records := parseNumstatZ([]byte("1\t2\tapp/main.go\x00-\t-\tbinary.dat\x003\t4\t\x00old/name.go\x00new/name.go\x00"))
	require.Len(t, records, 3)
	require.Equal(t, "app/main.go", records[0].Path)
	require.Equal(t, 1, records[0].Additions)
	require.Equal(t, "binary.dat", records[1].Path)
	require.True(t, records[1].Binary)
	require.Equal(t, "old/name.go", records[2].PreviousPath)
	require.Equal(t, "new/name.go", records[2].Path)
}

func writeFakeGitBinary(t *testing.T) string {
	t.Helper()

	scriptPath := filepath.Join(t.TempDir(), "git")
	script := `#!/bin/sh
set -eu
if [ "${1:-}" = "-C" ]; then
  shift 2
fi
cmd="${1:-}"
case "$cmd" in
  fetch|init|remote)
    exit 0
    ;;
  for-each-ref)
    printf 'refs/pull/17/head\000pullsha\000commit\000\000pullsha\000'
    printf 'refs/remotes/origin/main\000basesha\000commit\000\000basesha\000'
    exit 0
    ;;
  *)
    exit 0
    ;;
esac
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o755))
	return scriptPath
}
