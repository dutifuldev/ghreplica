package ghr

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gh "github.com/dutifuldev/ghreplica/internal/github"
	"github.com/dutifuldev/ghreplica/internal/gitindex"
	"github.com/dutifuldev/ghreplica/internal/testfixtures"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestRepoViewHumanOutput(t *testing.T) {
	server := newTestServer(t)
	cmd := NewRootCmd()
	stdout, stderr, err := executeCommand(cmd, "--base-url", server.URL, "repo", "view", "acme/widgets")
	require.NoError(t, err)
	require.Empty(t, stderr)
	require.Contains(t, stdout, "acme/widgets")
	require.Contains(t, stdout, "https://github.com/acme/widgets")
}

func TestRepoStatusHumanOutput(t *testing.T) {
	server := newTestServer(t)
	cmd := NewRootCmd()
	stdout, _, err := executeCommand(cmd, "--base-url", server.URL, "--repo", "acme/widgets", "repo", "status")
	require.NoError(t, err)
	require.Contains(t, stdout, "acme/widgets")
	require.Contains(t, stdout, "Sync mode:")
	require.Contains(t, stdout, "webhook_only")
	require.Contains(t, stdout, "PR review comments:")
}

func TestIssueListHumanOutput(t *testing.T) {
	server := newTestServer(t)
	cmd := NewRootCmd()
	stdout, _, err := executeCommand(cmd, "--base-url", server.URL, "--repo", "acme/widgets", "issue", "list", "--state", "all", "--limit", "10")
	require.NoError(t, err)
	require.Contains(t, stdout, "NUMBER")
	require.Contains(t, stdout, "#1")
	require.Contains(t, stdout, "Broken thing")
}

func TestIssueViewJSONOutput(t *testing.T) {
	server := newTestServer(t)
	cmd := NewRootCmd()
	stdout, _, err := executeCommand(cmd, "--base-url", server.URL, "--repo", "acme/widgets", "issue", "view", "1", "--json", "number,title,state")
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &payload))
	require.EqualValues(t, 1, payload["number"])
	require.Equal(t, "Broken thing", payload["title"])
	require.Equal(t, "open", payload["state"])
	require.Len(t, payload, 3)
}

func TestIssueViewCommentsOutput(t *testing.T) {
	server := newTestServer(t)
	cmd := NewRootCmd()
	stdout, _, err := executeCommand(cmd, "--base-url", server.URL, "--repo", "acme/widgets", "issue", "view", "1", "--comments")
	require.NoError(t, err)
	require.Contains(t, stdout, "Comments")
	require.Contains(t, stdout, "I can reproduce this.")
}

func TestIssueCommentsHumanOutput(t *testing.T) {
	server := newTestServer(t)
	cmd := NewRootCmd()
	stdout, _, err := executeCommand(cmd, "--base-url", server.URL, "--repo", "acme/widgets", "issue", "comments", "1")
	require.NoError(t, err)
	require.Contains(t, stdout, "octocat commented")
	require.Contains(t, stdout, "I can reproduce this.")
}

func TestPRListHumanOutput(t *testing.T) {
	server := newTestServer(t)
	cmd := NewRootCmd()
	stdout, _, err := executeCommand(cmd, "--base-url", server.URL, "--repo", "acme/widgets", "pr", "list", "--state", "all", "--limit", "10")
	require.NoError(t, err)
	require.Contains(t, stdout, "#2")
	require.Contains(t, stdout, "Fix parser")
}

func TestPRViewJSONOutput(t *testing.T) {
	server := newTestServer(t)
	cmd := NewRootCmd()
	stdout, _, err := executeCommand(cmd, "--base-url", server.URL, "--repo", "acme/widgets", "pr", "view", "2", "--json", "number,title,head,base")
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &payload))
	require.EqualValues(t, 2, payload["number"])
	require.Equal(t, "Fix parser", payload["title"])
	require.Contains(t, payload, "head")
	require.Contains(t, payload, "base")
}

func TestPRViewCommentsOutput(t *testing.T) {
	server := newTestServer(t)
	cmd := NewRootCmd()
	stdout, _, err := executeCommand(cmd, "--base-url", server.URL, "--repo", "acme/widgets", "pr", "view", "2", "--comments")
	require.NoError(t, err)
	require.Contains(t, stdout, "Fix parser")
	require.Contains(t, stdout, "Comments")
	require.Contains(t, stdout, "I can reproduce this.")
}

func TestIssueAndPRCommandsWithRealFixtures(t *testing.T) {
	server := newOpenClawTestServer(t)
	cmd := NewRootCmd()

	stdout, _, err := executeCommand(cmd, "--base-url", server.URL, "--repo", "openclaw/openclaw", "issue", "view", "66797", "--comments")
	require.NoError(t, err)
	require.Contains(t, stdout, "Group natural-language messages silently dropped")
	require.Contains(t, stdout, "kpiyush88 commented")
	require.Contains(t, stdout, "Still broken in 2026.4.14")

	cmd = NewRootCmd()
	stdout, _, err = executeCommand(cmd, "--base-url", server.URL, "--repo", "openclaw/openclaw", "pr", "view", "66863", "--comments")
	require.NoError(t, err)
	require.Contains(t, stdout, "fix(whatsapp): atomic auth state + socket keepalive tuning")
	require.Contains(t, stdout, "Greptile Summary")

	cmd = NewRootCmd()
	stdout, _, err = executeCommand(cmd, "--base-url", server.URL, "--repo", "openclaw/openclaw", "pr", "reviews", "66863")
	require.NoError(t, err)
	require.Contains(t, stdout, "greptile-apps[bot] reviewed")
	require.Contains(t, stdout, "(no review body)")

	cmd = NewRootCmd()
	stdout, _, err = executeCommand(cmd, "--base-url", server.URL, "--repo", "openclaw/openclaw", "pr", "comments", "66863")
	require.NoError(t, err)
	require.Contains(t, stdout, "greptile-apps[bot] commented on extensions/whatsapp/src/use-atomic-auth-state.ts:204")
	require.Contains(t, stdout, "auth-state.json")
}

func TestPRReviewAndCommentJSONOutputWithRealFixtures(t *testing.T) {
	server := newOpenClawTestServer(t)
	cmd := NewRootCmd()

	stdout, _, err := executeCommand(cmd, "--base-url", server.URL, "--repo", "openclaw/openclaw", "pr", "reviews", "66863", "--json", "id,state,user")
	require.NoError(t, err)
	var reviews []map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &reviews))
	require.Len(t, reviews, 1)
	require.EqualValues(t, 4109827861, reviews[0]["id"])
	require.Equal(t, "COMMENTED", reviews[0]["state"])
	require.Equal(t, "greptile-apps[bot]", reviews[0]["user"].(map[string]any)["login"])

	cmd = NewRootCmd()
	stdout, _, err = executeCommand(cmd, "--base-url", server.URL, "--repo", "openclaw/openclaw", "pr", "comments", "66863", "--json", "id,body,user,path")
	require.NoError(t, err)
	var comments []map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &comments))
	require.Len(t, comments, 2)
	require.EqualValues(t, 3083064505, comments[0]["id"])
	require.Equal(t, "extensions/whatsapp/src/use-atomic-auth-state.ts", comments[0]["path"])
	require.Equal(t, "greptile-apps[bot]", comments[0]["user"].(map[string]any)["login"])
}

func TestPRReviewsAndCommentsEmptyOutput(t *testing.T) {
	server := newTestServer(t)
	cmd := NewRootCmd()

	stdout, _, err := executeCommand(cmd, "--base-url", server.URL, "--repo", "acme/widgets", "pr", "reviews", "3")
	require.NoError(t, err)
	require.Contains(t, stdout, "no reviews found")

	stdout, _, err = executeCommand(cmd, "--base-url", server.URL, "--repo", "acme/widgets", "pr", "comments", "3")
	require.NoError(t, err)
	require.Contains(t, stdout, "no review comments found")
}

func TestChangesPRAndCompareCommands(t *testing.T) {
	server := newTestServer(t)
	cmd := NewRootCmd()

	stdout, _, err := executeCommand(cmd, "--base-url", server.URL, "--repo", "acme/widgets", "changes", "pr", "view", "2")
	require.NoError(t, err)
	require.Contains(t, stdout, "acme/widgets#2 change snapshot")
	require.Contains(t, stdout, "Indexed as:")
	require.Contains(t, stdout, "current")

	cmd = NewRootCmd()
	stdout, _, err = executeCommand(cmd, "--base-url", server.URL, "--repo", "acme/widgets", "changes", "pr", "files", "2")
	require.NoError(t, err)
	require.Contains(t, stdout, "PATH")
	require.Contains(t, stdout, "src/parser.ts")
	require.Contains(t, stdout, "test/parser_test.ts")

	cmd = NewRootCmd()
	stdout, _, err = executeCommand(cmd, "--base-url", server.URL, "--repo", "acme/widgets", "changes", "compare", "main...abc123")
	require.NoError(t, err)
	require.Contains(t, stdout, "acme/widgets compare main...abc123")
	require.Contains(t, stdout, "Snapshot PR:")
	require.Contains(t, stdout, "#2")
}

func TestChangesStatusCommands(t *testing.T) {
	server := newTestServer(t)
	cmd := NewRootCmd()

	stdout, _, err := executeCommand(cmd, "--base-url", server.URL, "--repo", "acme/widgets", "changes", "repo", "status")
	require.NoError(t, err)
	require.Contains(t, stdout, "acme/widgets change status")
	require.Contains(t, stdout, "Backfill mode:")
	require.Contains(t, stdout, "open_only")
	require.Contains(t, stdout, "Fetch owner:")
	require.Contains(t, stdout, "Backfill owner:")

	cmd = NewRootCmd()
	stdout, _, err = executeCommand(cmd, "--base-url", server.URL, "--repo", "acme/widgets", "changes", "pr", "status", "2")
	require.NoError(t, err)
	require.Contains(t, stdout, "acme/widgets#2 change status")
	require.Contains(t, stdout, "Indexed:")
	require.Contains(t, stdout, "current")
}

func TestChangesComparePreservesSlashRefs(t *testing.T) {
	server := newTestServer(t)
	cmd := NewRootCmd()

	stdout, _, err := executeCommand(cmd, "--base-url", server.URL, "--repo", "acme/widgets", "changes", "compare", "release/2026.04...abc123")
	require.NoError(t, err)
	require.Contains(t, stdout, "acme/widgets compare release/2026.04...abc123")
	require.Contains(t, stdout, "Snapshot PR:")
	require.Contains(t, stdout, "#9")
}

func TestChangesCommitCommands(t *testing.T) {
	server := newTestServer(t)
	cmd := NewRootCmd()

	stdout, _, err := executeCommand(cmd, "--base-url", server.URL, "--repo", "acme/widgets", "changes", "commit", "view", "abc123")
	require.NoError(t, err)
	require.Contains(t, stdout, "acme/widgets commit abc123")
	require.Contains(t, stdout, "Fix parser")

	cmd = NewRootCmd()
	stdout, _, err = executeCommand(cmd, "--base-url", server.URL, "--repo", "acme/widgets", "changes", "commit", "files", "abc123", "--json", "parent_sha,file")
	require.NoError(t, err)
	var payload []map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &payload))
	require.Len(t, payload, 2)
	require.Equal(t, "def456", payload[0]["parent_sha"])
}

func TestSearchCommands(t *testing.T) {
	server := newTestServer(t)
	cmd := NewRootCmd()

	stdout, _, err := executeCommand(cmd, "--base-url", server.URL, "--repo", "acme/widgets", "search", "related-prs", "2", "--mode", "path_overlap", "--state", "all")
	require.NoError(t, err)
	require.Contains(t, stdout, "#7")
	require.Contains(t, stdout, "draft")
	require.Contains(t, stdout, "paths=src/parser.ts")

	cmd = NewRootCmd()
	stdout, _, err = executeCommand(cmd, "--base-url", server.URL, "--repo", "acme/widgets", "search", "prs-by-paths", "--path", "src/parser.ts", "--path", "test/parser_test.ts", "--json", "pull_request_number,shared_paths")
	require.NoError(t, err)
	var pathMatches []map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &pathMatches))
	require.Len(t, pathMatches, 2)
	require.EqualValues(t, 7, pathMatches[0]["pull_request_number"])

	cmd = NewRootCmd()
	stdout, _, err = executeCommand(cmd, "--base-url", server.URL, "--repo", "acme/widgets", "search", "prs-by-ranges", "--path", "src/parser.ts", "--start", "11", "--end", "18")
	require.NoError(t, err)
	require.Contains(t, stdout, "#7")
	require.Contains(t, stdout, "overlapping_hunks=2")
}

func TestSearchByRangesRejectsMismatchedFlags(t *testing.T) {
	server := newTestServer(t)
	cmd := NewRootCmd()

	_, _, err := executeCommand(cmd, "--base-url", server.URL, "--repo", "acme/widgets", "search", "prs-by-ranges", "--path", "src/parser.ts", "--start", "11")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--path, --start, and --end must be provided the same number of times")
}

func TestIssueAndPRCommandsRejectPositionalRepoArgs(t *testing.T) {
	server := newTestServer(t)

	issueListCmd := NewRootCmd()
	_, _, err := executeCommand(issueListCmd, "--base-url", server.URL, "issue", "list", "acme/widgets")
	require.Error(t, err)
	require.Contains(t, err.Error(), "acme/widgets")

	issueViewCmd := NewRootCmd()
	_, _, err = executeCommand(issueViewCmd, "--base-url", server.URL, "issue", "view", "acme/widgets", "1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "received 2")

	prListCmd := NewRootCmd()
	_, _, err = executeCommand(prListCmd, "--base-url", server.URL, "pr", "list", "acme/widgets")
	require.Error(t, err)
	require.Contains(t, err.Error(), "acme/widgets")

	prViewCmd := NewRootCmd()
	_, _, err = executeCommand(prViewCmd, "--base-url", server.URL, "pr", "view", "acme/widgets", "2")
	require.Error(t, err)
	require.Contains(t, err.Error(), "received 2")
}

func TestWebFlagsOpenBrowser(t *testing.T) {
	server := newTestServer(t)
	opened := []string{}
	original := openURL
	openURL = func(target string) error {
		opened = append(opened, target)
		return nil
	}
	defer func() { openURL = original }()

	tests := []struct {
		args   []string
		target string
	}{
		{[]string{"--base-url", server.URL, "repo", "view", "acme/widgets", "--web"}, "https://github.com/acme/widgets"},
		{[]string{"--base-url", server.URL, "--repo", "acme/widgets", "issue", "view", "1", "--web"}, "https://github.com/acme/widgets/issues/1"},
		{[]string{"--base-url", server.URL, "--repo", "acme/widgets", "pr", "view", "2", "--web"}, "https://github.com/acme/widgets/pull/2"},
	}

	for _, tc := range tests {
		cmd := NewRootCmd()
		_, _, err := executeCommand(cmd, tc.args...)
		require.NoError(t, err)
	}

	require.Equal(t, []string{
		"https://github.com/acme/widgets",
		"https://github.com/acme/widgets/issues/1",
		"https://github.com/acme/widgets/pull/2",
	}, opened)
}

func executeCommand(cmd *cobra.Command, args ...string) (string, string, error) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	repo := repoFixture()
	issues := []gh.IssueResponse{issueFixture()}
	pulls := []gh.PullRequestResponse{pullFixture()}
	issueComments := []gh.IssueCommentResponse{issueCommentFixture()}
	reviews := []gh.PullRequestReviewResponse{{}}
	reviewComments := []gh.PullRequestReviewCommentResponse{{}}
	status := MirrorStatusResponse{
		FullName:                 "acme/widgets",
		RepositoryPresent:        true,
		TrackedRepositoryPresent: true,
		Enabled:                  true,
		SyncMode:                 "webhook_only",
		WebhookProjectionEnabled: true,
		AllowManualBackfill:      true,
		IssuesCompleteness:       "sparse",
		PullsCompleteness:        "sparse",
		CommentsCompleteness:     "sparse",
		ReviewsCompleteness:      "sparse",
		Counts: MirrorCountsResponse{
			Issues:                    1,
			Pulls:                     1,
			IssueComments:             1,
			PullRequestReviews:        0,
			PullRequestReviewComments: 0,
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/github/repos/acme/widgets", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, repo)
	})
	mux.HandleFunc("/repos/acme/widgets/_ghreplica", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, status)
	})
	mux.HandleFunc("/v1/github/repos/acme/widgets/issues", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, issues)
	})
	mux.HandleFunc("/v1/github/repos/acme/widgets/issues/1", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, issues[0])
	})
	mux.HandleFunc("/v1/github/repos/acme/widgets/issues/1/comments", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, issueComments)
	})
	mux.HandleFunc("/v1/github/repos/acme/widgets/pulls", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, pulls)
	})
	mux.HandleFunc("/v1/github/repos/acme/widgets/pulls/2", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, pulls[0])
	})
	mux.HandleFunc("/v1/github/repos/acme/widgets/issues/2/comments", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, issueComments)
	})
	mux.HandleFunc("/v1/github/repos/acme/widgets/pulls/2/reviews", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, reviews[:0])
	})
	mux.HandleFunc("/v1/github/repos/acme/widgets/pulls/2/comments", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, reviewComments[:0])
	})
	mux.HandleFunc("/v1/github/repos/acme/widgets/pulls/3/reviews", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, reviews[:0])
	})
	mux.HandleFunc("/v1/github/repos/acme/widgets/pulls/3/comments", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, reviewComments[:0])
	})
	mux.HandleFunc("/v1/changes/repos/acme/widgets/pulls/2", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, pullRequestChangeSnapshotFixture())
	})
	mux.HandleFunc("/v1/changes/repos/acme/widgets/status", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, gitindex.RepoStatus{
			RepositoryID:       101,
			FullName:           "acme/widgets",
			Dirty:              true,
			BackfillMode:       "open_only",
			BackfillPriority:   5,
			OpenPRTotal:        3,
			OpenPRCurrent:      1,
			OpenPRStale:        1,
			OpenPRMissing:      1,
			FetchInProgress:    false,
			BackfillInProgress: true,
		})
	})
	mux.HandleFunc("/v1/changes/repos/acme/widgets/pulls/2/status", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, gitindex.PullRequestStatus{
			RepositoryID:      101,
			PullRequestNumber: 2,
			State:             "open",
			Indexed:           true,
			HeadSHA:           "abc123",
			BaseSHA:           "main456",
			MergeBaseSHA:      "main456",
			BaseRef:           "main",
			IndexedAs:         "full",
			IndexFreshness:    "current",
			ChangedFiles:      2,
			IndexedFileCount:  2,
			HunkCount:         4,
		})
	})
	mux.HandleFunc("/v1/changes/repos/acme/widgets/pulls/2/files", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, fileChangesFixture())
	})
	mux.HandleFunc("/v1/changes/repos/acme/widgets/commits/abc123", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, commitFixture())
	})
	mux.HandleFunc("/v1/changes/repos/acme/widgets/commits/abc123/files", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, commitFilesFixture())
	})
	mux.HandleFunc("/v1/changes/repos/acme/widgets/compare/main...abc123", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, compareFixture())
	})
	mux.HandleFunc("/v1/changes/repos/acme/widgets/compare/release%2F2026.04...abc123", func(w http.ResponseWriter, r *http.Request) {
		resp := compareFixture()
		resp.Base = "release/2026.04"
		resp.Resolved.Base = "rel123"
		resp.Snapshot.PullRequestNumber = 9
		resp.Snapshot.BaseRef = "release/2026.04"
		writeResponseJSON(t, w, resp)
	})
	mux.HandleFunc("/v1/search/repos/acme/widgets/pulls/2/related", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, searchMatchesFixture())
	})
	mux.HandleFunc("/v1/search/repos/acme/widgets/pulls/by-paths", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, searchMatchesFixture())
	})
	mux.HandleFunc("/v1/search/repos/acme/widgets/pulls/by-ranges", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, []gitindex.SearchMatch{searchMatchesFixture()[0]})
	})

	return httptest.NewServer(mux)
}

func newOpenClawTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	repo := testfixtures.OpenClawRepository(t)
	issue := testfixtures.OpenClawIssue66797(t)
	issueComments := testfixtures.OpenClawIssue66797Comments(t)
	prIssueComments := testfixtures.OpenClawIssue66863Comments(t)
	pull := testfixtures.OpenClawPull66863(t)
	reviews := testfixtures.OpenClawPull66863Reviews(t)
	reviewComments := testfixtures.OpenClawPull66863ReviewComments(t)
	status := MirrorStatusResponse{
		FullName:                 "openclaw/openclaw",
		RepositoryPresent:        true,
		TrackedRepositoryPresent: true,
		Enabled:                  true,
		SyncMode:                 "webhook_only",
		WebhookProjectionEnabled: true,
		AllowManualBackfill:      false,
		IssuesCompleteness:       "sparse",
		PullsCompleteness:        "sparse",
		CommentsCompleteness:     "sparse",
		ReviewsCompleteness:      "sparse",
		Counts: MirrorCountsResponse{
			Issues:                    2,
			Pulls:                     1,
			IssueComments:             2,
			PullRequestReviews:        1,
			PullRequestReviewComments: 2,
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/github/repos/openclaw/openclaw", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, repo)
	})
	mux.HandleFunc("/repos/openclaw/openclaw/_ghreplica", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, status)
	})
	mux.HandleFunc("/v1/github/repos/openclaw/openclaw/issues", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, []gh.IssueResponse{issue})
	})
	mux.HandleFunc("/v1/github/repos/openclaw/openclaw/issues/66797", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, issue)
	})
	mux.HandleFunc("/v1/github/repos/openclaw/openclaw/issues/66797/comments", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, issueComments)
	})
	mux.HandleFunc("/v1/github/repos/openclaw/openclaw/pulls", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, []gh.PullRequestResponse{pull})
	})
	mux.HandleFunc("/v1/github/repos/openclaw/openclaw/pulls/66863", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, pull)
	})
	mux.HandleFunc("/v1/github/repos/openclaw/openclaw/issues/66863/comments", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, prIssueComments)
	})
	mux.HandleFunc("/v1/github/repos/openclaw/openclaw/pulls/66863/reviews", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, reviews)
	})
	mux.HandleFunc("/v1/github/repos/openclaw/openclaw/pulls/66863/comments", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, reviewComments)
	})

	return httptest.NewServer(mux)
}

func writeResponseJSON(t *testing.T, w http.ResponseWriter, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(payload))
}

func repoFixture() gh.RepositoryResponse {
	now := time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC)
	return gh.RepositoryResponse{
		ID:            101,
		NodeID:        "R_kgDORepo",
		Name:          "widgets",
		FullName:      "acme/widgets",
		Private:       false,
		Owner:         &gh.UserResponse{ID: 11, NodeID: "U_owner", Login: "acme", Type: "Organization", AvatarURL: "https://example.com/acme.png", HTMLURL: "https://github.com/acme", URL: "https://api.github.com/users/acme"},
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

func issueFixture() gh.IssueResponse {
	now := time.Date(2026, 4, 14, 13, 0, 0, 0, time.UTC)
	return gh.IssueResponse{
		ID:        201,
		NodeID:    "I_kgDO1",
		Number:    1,
		Title:     "Broken thing",
		Body:      "Something is broken.",
		State:     "open",
		User:      &gh.UserResponse{ID: 21, NodeID: "U_author", Login: "octocat", Type: "User", AvatarURL: "https://example.com/octocat.png", HTMLURL: "https://github.com/octocat", URL: "https://api.github.com/users/octocat"},
		Comments:  1,
		HTMLURL:   "https://github.com/acme/widgets/issues/1",
		URL:       "https://api.github.com/repos/acme/widgets/issues/1",
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func issueCommentFixture() gh.IssueCommentResponse {
	now := time.Date(2026, 4, 14, 13, 30, 0, 0, time.UTC)
	return gh.IssueCommentResponse{
		ID:        301,
		NodeID:    "IC_kgDO1",
		Body:      "I can reproduce this.",
		User:      &gh.UserResponse{ID: 21, NodeID: "U_author", Login: "octocat", Type: "User", AvatarURL: "https://example.com/octocat.png", HTMLURL: "https://github.com/octocat", URL: "https://api.github.com/users/octocat"},
		IssueURL:  "https://api.github.com/repos/acme/widgets/issues/1",
		HTMLURL:   "https://github.com/acme/widgets/issues/1#issuecomment-301",
		URL:       "https://api.github.com/repos/acme/widgets/issues/comments/301",
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func pullFixture() gh.PullRequestResponse {
	now := time.Date(2026, 4, 14, 14, 0, 0, 0, time.UTC)
	repo := gh.PullBranchRepository{
		ID:            101,
		NodeID:        "R_kgDORepo",
		Name:          "widgets",
		FullName:      "acme/widgets",
		Private:       false,
		Owner:         &gh.UserResponse{ID: 11, NodeID: "U_owner", Login: "acme", Type: "Organization", AvatarURL: "https://example.com/acme.png", HTMLURL: "https://github.com/acme", URL: "https://api.github.com/users/acme"},
		HTMLURL:       "https://github.com/acme/widgets",
		Description:   "Widget tracker",
		Fork:          false,
		URL:           "https://api.github.com/repos/acme/widgets",
		DefaultBranch: "main",
		Visibility:    "public",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	return gh.PullRequestResponse{
		ID:             202,
		NodeID:         "PR_kgDO2",
		Number:         2,
		State:          "open",
		Title:          "Fix parser",
		Body:           "Need to handle edge cases.",
		User:           &gh.UserResponse{ID: 21, NodeID: "U_author", Login: "octocat", Type: "User", AvatarURL: "https://example.com/octocat.png", HTMLURL: "https://github.com/octocat", URL: "https://api.github.com/users/octocat"},
		Draft:          false,
		Head:           gh.PullBranch{Ref: "fix/parser", SHA: "abc123", Repo: &repo},
		Base:           gh.PullBranch{Ref: "main", SHA: "def456", Repo: &repo},
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
	}
}

func boolPtr(v bool) *bool { return &v }

func pullRequestChangeSnapshotFixture() PullRequestChangeSnapshotResponse {
	now := time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC)
	return PullRequestChangeSnapshotResponse{
		PullRequestNumber: 2,
		HeadSHA:           "abc123",
		BaseSHA:           "def456",
		MergeBaseSHA:      "def456",
		BaseRef:           "main",
		State:             "open",
		Draft:             false,
		IndexedAs:         "full",
		IndexFreshness:    "current",
		PathCount:         2,
		IndexedFileCount:  2,
		HunkCount:         3,
		Additions:         10,
		Deletions:         2,
		PatchBytes:        128,
		LastIndexedAt:     &now,
	}
}

func fileChangesFixture() []gitindex.FileChange {
	return []gitindex.FileChange{
		{
			Path:        "src/parser.ts",
			Status:      "modified",
			FileKind:    "text",
			IndexedAs:   "full",
			HeadBlobSHA: "111111",
			BaseBlobSHA: "222222",
			Additions:   8,
			Deletions:   1,
			Changes:     9,
			Hunks: []gitindex.Hunk{
				{Index: 0, DiffHunk: "@@ -11,2 +11,5 @@", OldStart: 11, OldCount: 2, OldEnd: 12, NewStart: 11, NewCount: 5, NewEnd: 15},
				{Index: 1, DiffHunk: "@@ -21,1 +24,2 @@", OldStart: 21, OldCount: 1, OldEnd: 21, NewStart: 24, NewCount: 2, NewEnd: 25},
			},
		},
		{
			Path:        "test/parser_test.ts",
			Status:      "modified",
			FileKind:    "text",
			IndexedAs:   "full",
			HeadBlobSHA: "333333",
			BaseBlobSHA: "444444",
			Additions:   2,
			Deletions:   1,
			Changes:     3,
			Hunks: []gitindex.Hunk{
				{Index: 0, DiffHunk: "@@ -5,1 +5,2 @@", OldStart: 5, OldCount: 1, OldEnd: 5, NewStart: 5, NewCount: 2, NewEnd: 6},
			},
		},
	}
}

func commitFixture() CommitResponse {
	now := time.Date(2026, 4, 15, 9, 55, 0, 0, time.UTC)
	return CommitResponse{
		SHA:             "abc123",
		TreeSHA:         "tree123",
		AuthorName:      "Octo Cat",
		AuthorEmail:     "octo@example.com",
		AuthoredAt:      now,
		CommitterName:   "Octo Cat",
		CommitterEmail:  "octo@example.com",
		CommittedAt:     now,
		Message:         "Fix parser",
		MessageEncoding: "UTF-8",
		Parents:         []string{"def456"},
	}
}

func commitFilesFixture() []map[string]any {
	files := fileChangesFixture()
	return []map[string]any{
		{
			"parent_sha":   "def456",
			"parent_index": 0,
			"file": map[string]any{
				"path":          files[0].Path,
				"status":        files[0].Status,
				"file_kind":     files[0].FileKind,
				"indexed_as":    files[0].IndexedAs,
				"additions":     files[0].Additions,
				"deletions":     files[0].Deletions,
				"changes":       files[0].Changes,
				"head_blob_sha": files[0].HeadBlobSHA,
				"base_blob_sha": files[0].BaseBlobSHA,
			},
		},
		{
			"parent_sha":   "def456",
			"parent_index": 0,
			"file": map[string]any{
				"path":          files[1].Path,
				"status":        files[1].Status,
				"file_kind":     files[1].FileKind,
				"indexed_as":    files[1].IndexedAs,
				"additions":     files[1].Additions,
				"deletions":     files[1].Deletions,
				"changes":       files[1].Changes,
				"head_blob_sha": files[1].HeadBlobSHA,
				"base_blob_sha": files[1].BaseBlobSHA,
			},
		},
	}
}

func compareFixture() CompareResponse {
	resp := CompareResponse{
		Base:     "main",
		Head:     "abc123",
		Snapshot: pullRequestChangeSnapshotFixture(),
		Files:    fileChangesFixture(),
	}
	resp.Resolved.Base = "def456"
	resp.Resolved.Head = "abc123"
	return resp
}

func searchMatchesFixture() []gitindex.SearchMatch {
	return []gitindex.SearchMatch{
		{
			PullRequestNumber: 7,
			State:             "open",
			Draft:             true,
			HeadSHA:           "fedcba",
			BaseRef:           "main",
			IndexedAs:         "full",
			IndexFreshness:    "current",
			Score:             24,
			SharedPaths:       []string{"src/parser.ts", "test/parser_test.ts"},
			OverlappingHunks:  2,
			MatchedRanges: []gitindex.MatchedPath{
				{Path: "src/parser.ts", NewStart: 11, NewEnd: 18},
			},
			Reasons: []string{"shared_paths", "range_overlap"},
		},
		{
			PullRequestNumber: 8,
			State:             "closed",
			Draft:             false,
			HeadSHA:           "beaded",
			BaseRef:           "main",
			IndexedAs:         "paths_only",
			IndexFreshness:    "current",
			Score:             12,
			SharedPaths:       []string{"src/parser.ts"},
			Reasons:           []string{"shared_paths"},
		},
	}
}
