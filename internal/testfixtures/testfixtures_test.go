package testfixtures

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenClawFixturesAndHandler(t *testing.T) {
	repo := OpenClawRepository(t)
	require.Equal(t, "openclaw/openclaw", repo.FullName)

	issue := OpenClawIssue66797(t)
	require.Equal(t, 66797, issue.Number)

	issueComments := OpenClawIssue66797Comments(t)
	require.NotEmpty(t, issueComments)

	require.Equal(t, 66863, OpenClawIssue66863(t).Number)
	require.Equal(t, 67094, OpenClawIssue67094Closed(t).Number)
	require.NotEmpty(t, OpenClawIssue66863Comments(t))

	pull := OpenClawPull66863(t)
	require.Equal(t, 66863, pull.Number)
	require.Equal(t, 67079, OpenClawPull67079Closed(t).Number)
	require.Equal(t, 67096, OpenClawPull67096Open(t).Number)
	require.NotEmpty(t, OpenClawPull66863Reviews(t))
	require.NotEmpty(t, OpenClawPull66863ReviewComments(t))

	server := httptest.NewServer(NewOpenClawGitHubHandler(t))
	t.Cleanup(server.Close)

	for _, tc := range []struct {
		path string
		into any
	}{
		{path: "/repos/openclaw/openclaw", into: &map[string]any{}},
		{path: "/repos/openclaw/openclaw/issues/66797", into: &map[string]any{}},
		{path: "/repos/openclaw/openclaw/issues/66797/comments", into: &[]map[string]any{}},
		{path: "/repos/openclaw/openclaw/issues/66863", into: &map[string]any{}},
		{path: "/repos/openclaw/openclaw/issues/66863/comments", into: &[]map[string]any{}},
		{path: "/repos/openclaw/openclaw/pulls/66863", into: &map[string]any{}},
		{path: "/repos/openclaw/openclaw/pulls/66863/reviews", into: &[]map[string]any{}},
		{path: "/repos/openclaw/openclaw/pulls/66863/comments", into: &[]map[string]any{}},
	} {
		resp, err := http.Get(server.URL + tc.path)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "application/json", resp.Header.Get("Content-Type"))
		require.NoError(t, json.NewDecoder(resp.Body).Decode(tc.into))
		require.NoError(t, resp.Body.Close())
	}
}

func TestCreateLocalPullRepo(t *testing.T) {
	repo := CreateLocalPullRepo(t)

	require.True(t, strings.HasPrefix(repo.RemoteURL, "file://"))
	require.Len(t, repo.Pulls, 3)
	require.Len(t, repo.BaseSHA, 40)

	remotePath := strings.TrimPrefix(repo.RemoteURL, "file://")
	for number, pull := range repo.Pulls {
		require.Equal(t, number, pull.Number)
		require.NotEmpty(t, pull.HeadRef)
		require.Len(t, pull.HeadSHA, 40)
	}

	showRef := exec.Command("git", "--git-dir", filepath.Clean(remotePath), "show-ref")
	out, err := showRef.CombinedOutput()
	require.NoError(t, err, string(out))
	text := string(out)
	require.Contains(t, text, "refs/heads/main")
	require.Contains(t, text, "refs/pull/101/head")
	require.Contains(t, text, "refs/pull/102/head")
	require.Contains(t, text, "refs/pull/103/head")

	customPath := filepath.Join(t.TempDir(), "nested", "file.txt")
	writeFile(t, customPath, "hello\n")
	body, err := os.ReadFile(customPath)
	require.NoError(t, err)
	require.Equal(t, "hello\n", string(body))
}
