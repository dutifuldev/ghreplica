package ghr

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gh "github.com/dutifuldev/ghreplica/internal/github"
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
	mux.HandleFunc("/repos/acme/widgets", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, repo)
	})
	mux.HandleFunc("/repos/acme/widgets/_ghreplica", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, status)
	})
	mux.HandleFunc("/repos/acme/widgets/issues", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, issues)
	})
	mux.HandleFunc("/repos/acme/widgets/issues/1", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, issues[0])
	})
	mux.HandleFunc("/repos/acme/widgets/issues/1/comments", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, issueComments)
	})
	mux.HandleFunc("/repos/acme/widgets/pulls", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, pulls)
	})
	mux.HandleFunc("/repos/acme/widgets/pulls/2", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, pulls[0])
	})
	mux.HandleFunc("/repos/acme/widgets/issues/2/comments", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, issueComments)
	})
	mux.HandleFunc("/repos/acme/widgets/pulls/2/reviews", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, reviews[:0])
	})
	mux.HandleFunc("/repos/acme/widgets/pulls/2/comments", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, reviewComments[:0])
	})
	mux.HandleFunc("/repos/acme/widgets/pulls/3/reviews", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, reviews[:0])
	})
	mux.HandleFunc("/repos/acme/widgets/pulls/3/comments", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, reviewComments[:0])
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
	mux.HandleFunc("/repos/openclaw/openclaw", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, repo)
	})
	mux.HandleFunc("/repos/openclaw/openclaw/_ghreplica", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, status)
	})
	mux.HandleFunc("/repos/openclaw/openclaw/issues", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, []gh.IssueResponse{issue})
	})
	mux.HandleFunc("/repos/openclaw/openclaw/issues/66797", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, issue)
	})
	mux.HandleFunc("/repos/openclaw/openclaw/issues/66797/comments", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, issueComments)
	})
	mux.HandleFunc("/repos/openclaw/openclaw/pulls", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, []gh.PullRequestResponse{pull})
	})
	mux.HandleFunc("/repos/openclaw/openclaw/pulls/66863", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, pull)
	})
	mux.HandleFunc("/repos/openclaw/openclaw/issues/66863/comments", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, prIssueComments)
	})
	mux.HandleFunc("/repos/openclaw/openclaw/pulls/66863/reviews", func(w http.ResponseWriter, r *http.Request) {
		writeResponseJSON(t, w, reviews)
	})
	mux.HandleFunc("/repos/openclaw/openclaw/pulls/66863/comments", func(w http.ResponseWriter, r *http.Request) {
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
