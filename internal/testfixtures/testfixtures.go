package testfixtures

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	gh "github.com/dutifuldev/ghreplica/internal/github"
)

func LoadJSON[T any](t *testing.T, relativePath string) T {
	t.Helper()

	path := filepath.Join(repoRoot(t), "testdata", relativePath)
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", relativePath, err)
	}

	var out T
	if err := json.Unmarshal(payload, &out); err != nil {
		t.Fatalf("decode fixture %s: %v", relativePath, err)
	}
	return out
}

func OpenClawRepository(t *testing.T) gh.RepositoryResponse {
	return LoadJSON[gh.RepositoryResponse](t, "openclaw/repository.json")
}

func OpenClawIssue66797(t *testing.T) gh.IssueResponse {
	return LoadJSON[gh.IssueResponse](t, "openclaw/issue_66797.json")
}

func OpenClawIssue66797Comments(t *testing.T) []gh.IssueCommentResponse {
	return LoadJSON[[]gh.IssueCommentResponse](t, "openclaw/issue_66797_comments.json")
}

func OpenClawIssue66863(t *testing.T) gh.IssueResponse {
	return LoadJSON[gh.IssueResponse](t, "openclaw/issue_66863.json")
}

func OpenClawIssue66863Comments(t *testing.T) []gh.IssueCommentResponse {
	return LoadJSON[[]gh.IssueCommentResponse](t, "openclaw/issue_66863_comments.json")
}

func OpenClawPull66863(t *testing.T) gh.PullRequestResponse {
	return LoadJSON[gh.PullRequestResponse](t, "openclaw/pull_66863.json")
}

func OpenClawPull66863Reviews(t *testing.T) []gh.PullRequestReviewResponse {
	return LoadJSON[[]gh.PullRequestReviewResponse](t, "openclaw/pull_66863_reviews.json")
}

func OpenClawPull66863ReviewComments(t *testing.T) []gh.PullRequestReviewCommentResponse {
	return LoadJSON[[]gh.PullRequestReviewCommentResponse](t, "openclaw/pull_66863_review_comments.json")
}

func NewOpenClawGitHubHandler(t *testing.T) http.Handler {
	t.Helper()

	repo := OpenClawRepository(t)
	issue66797 := OpenClawIssue66797(t)
	issue66797Comments := OpenClawIssue66797Comments(t)
	issue66863 := OpenClawIssue66863(t)
	issue66863Comments := OpenClawIssue66863Comments(t)
	pull66863 := OpenClawPull66863(t)
	pull66863Reviews := OpenClawPull66863Reviews(t)
	pull66863ReviewComments := OpenClawPull66863ReviewComments(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/openclaw/openclaw", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, repo)
	})
	mux.HandleFunc("/repos/openclaw/openclaw/issues/66797", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, issue66797)
	})
	mux.HandleFunc("/repos/openclaw/openclaw/issues/66797/comments", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, issue66797Comments)
	})
	mux.HandleFunc("/repos/openclaw/openclaw/issues/66863", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, issue66863)
	})
	mux.HandleFunc("/repos/openclaw/openclaw/issues/66863/comments", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, issue66863Comments)
	})
	mux.HandleFunc("/repos/openclaw/openclaw/pulls/66863", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, pull66863)
	})
	mux.HandleFunc("/repos/openclaw/openclaw/pulls/66863/reviews", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, pull66863Reviews)
	})
	mux.HandleFunc("/repos/openclaw/openclaw/pulls/66863/comments", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, pull66863ReviewComments)
	})
	return mux
}

func repoRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve fixture root: runtime caller unavailable")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

func writeJSON(t testing.TB, w http.ResponseWriter, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatalf("encode fixture response: %v", err)
	}
}
