package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/dutifuldev/ghreplica/internal/gitindex"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type stubChangeStatus struct {
	repoStatus gitindex.RepoStatus
	pullStatus gitindex.PullRequestStatus
	repoErr    error
	pullErr    error
}

func (s stubChangeStatus) GetRepoChangeStatus(context.Context, string, string) (gitindex.RepoStatus, error) {
	return s.repoStatus, s.repoErr
}

func (s stubChangeStatus) GetPullRequestChangeStatus(context.Context, string, string, int) (gitindex.PullRequestStatus, error) {
	return s.pullStatus, s.pullErr
}

type stubStructuralSearch struct {
	response gitindex.StructuralSearchResponse
	err      error
	request  gitindex.StructuralSearchRequest
}

func (s *stubStructuralSearch) SearchStructural(_ context.Context, _ string, _ string, request gitindex.StructuralSearchRequest) (gitindex.StructuralSearchResponse, error) {
	s.request = request
	return s.response, s.err
}

func TestStoredGitHubResourceHandlers(t *testing.T) {
	t.Parallel()

	db := openHTTPAPITestDB(t)
	seedStoredGitHubReadData(t, db)
	server := NewServer(db, Options{})

	for _, tc := range []struct {
		path string
		want int
	}{
		{path: "/v1/github/repos/acme/widgets", want: http.StatusOK},
		{path: "/v1/github/repos/acme/widgets/issues", want: http.StatusOK},
		{path: "/v1/github/repos/acme/widgets/issues/7", want: http.StatusOK},
		{path: "/v1/github/repos/acme/widgets/issues/7/comments", want: http.StatusOK},
		{path: "/v1/github/repos/acme/widgets/pulls", want: http.StatusOK},
		{path: "/v1/github/repos/acme/widgets/pulls/7", want: http.StatusOK},
		{path: "/v1/github/repos/acme/widgets/pulls/7/reviews", want: http.StatusOK},
		{path: "/v1/github/repos/acme/widgets/pulls/7/comments", want: http.StatusOK},
		{path: "/v1/github/repos/acme/widgets/issues/not-a-number", want: http.StatusBadRequest},
		{path: "/v1/github/repos/acme/widgets/pulls/not-a-number", want: http.StatusBadRequest},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rec := httptest.NewRecorder()
		server.Echo().ServeHTTP(rec, req)
		require.Equal(t, tc.want, rec.Code, tc.path)
	}
}

func TestChangeStatusAndSnapshotHandlers(t *testing.T) {
	t.Parallel()

	db := openHTTPAPITestDB(t)
	seedStoredGitHubReadData(t, db)
	server := NewServer(db, Options{
		ChangeStatus: stubChangeStatus{
			repoStatus: gitindex.RepoStatus{FullName: "acme/widgets", InventoryNeedsRefresh: true},
			pullStatus: gitindex.PullRequestStatus{RepositoryID: 1, PullRequestNumber: 7},
		},
	})

	for _, tc := range []struct {
		path string
		want int
	}{
		{path: "/v1/changes/repos/acme/widgets/status", want: http.StatusOK},
		{path: "/v1/changes/repos/acme/widgets/pulls/7/status", want: http.StatusOK},
		{path: "/v1/changes/repos/acme/widgets/pulls/7", want: http.StatusOK},
		{path: "/v1/changes/repos/acme/widgets/pulls/7/files", want: http.StatusOK},
		{path: "/v1/changes/repos/acme/widgets/pulls/0/status", want: http.StatusBadRequest},
		{path: "/v1/changes/repos/acme/widgets/pulls/0", want: http.StatusBadRequest},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rec := httptest.NewRecorder()
		server.Echo().ServeHTTP(rec, req)
		require.Equal(t, tc.want, rec.Code, tc.path)
	}

	require.True(t, containsPath([]string{"app/service.go", "docs/readme.md"}, "docs/readme.md"))
	require.False(t, containsPath([]string{"app/service.go"}, "pkg/missing.txt"))
	require.Equal(t, 9, minNonZero(0, 9))
	require.True(t, rangeOverlap(5, 10, 8, 12))
}

func TestASTGrepAndSearchStatusHandlers(t *testing.T) {
	t.Parallel()

	db := openHTTPAPITestDB(t)
	seedStoredGitHubReadData(t, db)

	t.Run("search status unavailable", func(t *testing.T) {
		server := NewServer(db, Options{})
		server.search = nil
		req := httptest.NewRequest(http.MethodGet, "/v1/search/repos/acme/widgets/status", nil)
		rec := httptest.NewRecorder()
		server.Echo().ServeHTTP(rec, req)
		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})

	t.Run("structural search validation", func(t *testing.T) {
		search := &stubStructuralSearch{
			response: gitindex.StructuralSearchResponse{
				Repository:        gitindex.SearchRepository{Owner: "acme", Name: "widgets", FullName: "acme/widgets"},
				ResolvedCommitSHA: "head",
				Matches:           []gitindex.StructuralMatch{{Path: "app/service.go", Text: "run()"}},
			},
		}
		server := NewServer(db, Options{StructuralSearch: search})

		req := httptest.NewRequest(http.MethodPost, "/v1/search/repos/acme/widgets/ast-grep", bytes.NewReader([]byte(`{"pull_request_number":7,"language":"go","rule":{"pattern":"run()"}}`)))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		server.Echo().ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, 7, search.request.PullRequestNumber)

		search.err = errors.New("boom")
		req = httptest.NewRequest(http.MethodPost, "/v1/search/repos/acme/widgets/ast-grep", bytes.NewReader([]byte(`{"pull_request_number":7,"language":"go","rule":{"pattern":"run()"}}`)))
		req.Header.Set("Content-Type", "application/json")
		rec = httptest.NewRecorder()
		server.Echo().ServeHTTP(rec, req)
		require.Equal(t, http.StatusInternalServerError, rec.Code)

		server.structuralSearch = nil
		req = httptest.NewRequest(http.MethodPost, "/v1/search/repos/acme/widgets/ast-grep", bytes.NewReader([]byte(`{}`)))
		rec = httptest.NewRecorder()
		server.Echo().ServeHTTP(rec, req)
		require.Equal(t, http.StatusServiceUnavailable, rec.Code)

		search.err = nil
		server = NewServer(db, Options{StructuralSearch: search})
		req = httptest.NewRequest(http.MethodPost, "/v1/search/repos/acme/widgets/ast-grep", bytes.NewReader([]byte(`{`)))
		req.Header.Set("Content-Type", "application/json")
		rec = httptest.NewRecorder()
		server.Echo().ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

func TestChangeSearchValidationBranches(t *testing.T) {
	t.Parallel()

	db := openHTTPAPITestDB(t)
	seedStoredGitHubReadData(t, db)
	server := NewServer(db, Options{})

	req := httptest.NewRequest(http.MethodGet, "/v1/changes/repos/acme/widgets/status", nil)
	rec := httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)

	req = httptest.NewRequest(http.MethodGet, "/v1/changes/repos/acme/widgets/pulls/7/status", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)

	req = httptest.NewRequest(http.MethodGet, "/v1/changes/repos/acme/widgets/pulls/0/files", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	req = httptest.NewRequest(http.MethodGet, "/v1/changes/repos/acme/widgets/compare/main", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	req = httptest.NewRequest(http.MethodPost, "/v1/search/repos/acme/widgets/pulls/by-paths", bytes.NewBufferString(`{`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	req = httptest.NewRequest(http.MethodPost, "/v1/search/repos/acme/widgets/pulls/by-paths", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	req = httptest.NewRequest(http.MethodPost, "/v1/search/repos/acme/widgets/pulls/by-ranges", bytes.NewBufferString(`{`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	req = httptest.NewRequest(http.MethodPost, "/v1/search/repos/acme/widgets/pulls/by-ranges", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	server.search = nil

	req = httptest.NewRequest(http.MethodPost, "/v1/search/repos/acme/widgets/mentions", bytes.NewBufferString(`{"query":"review"}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)

	req = httptest.NewRequest(http.MethodGet, "/v1/search/repos/acme/widgets/status", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestLoadSnapshotAndStatusHelpers(t *testing.T) {
	t.Parallel()

	db := openHTTPAPITestDB(t)
	seedStoredGitHubReadData(t, db)
	server := NewServer(db, Options{ChangeStatus: stubChangeStatus{repoErr: gorm.ErrRecordNotFound, pullErr: gorm.ErrRecordNotFound}})

	var count int64
	require.NoError(t, applySnapshotStateFilter(db.Model(&database.PullRequestChangeSnapshot{}), "").Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, applySnapshotStateFilter(db.Model(&database.PullRequestChangeSnapshot{}), "all").Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, applySnapshotStateFilter(db.Model(&database.PullRequestChangeSnapshot{}), "closed").Count(&count).Error)
	require.Zero(t, count)

	req := httptest.NewRequest(http.MethodGet, "/v1/changes/repos/acme/widgets/status", nil)
	rec := httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)

	req = httptest.NewRequest(http.MethodGet, "/v1/changes/repos/acme/widgets/pulls/7/status", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)

	req = httptest.NewRequest(http.MethodGet, "/v1/changes/repos/acme/widgets/pulls/abc/status", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	c, _ := newServerTestContext(server, http.MethodGet, "/v1/changes/repos/acme/widgets/pulls/7", nil, []string{"owner", "repo", "number"}, []string{"acme", "widgets", "7"})
	repo, snapshot, err := server.loadSnapshot(c)
	require.NoError(t, err)
	require.Equal(t, "acme/widgets", repo.FullName)
	require.Equal(t, 7, snapshot.PullRequestNumber)

	c, _ = newServerTestContext(server, http.MethodGet, "/v1/changes/repos/missing/repo/pulls/7", nil, []string{"owner", "repo", "number"}, []string{"missing", "repo", "7"})
	_, _, err = server.loadSnapshot(c)
	require.Equal(t, http.StatusNotFound, httpErrorCode(t, err))

	c, _ = newServerTestContext(server, http.MethodGet, "/v1/changes/repos/acme/widgets/pulls/abc", nil, []string{"owner", "repo", "number"}, []string{"acme", "widgets", "abc"})
	_, _, err = server.loadSnapshot(c)
	require.Equal(t, http.StatusBadRequest, httpErrorCode(t, err))

	c, _ = newServerTestContext(server, http.MethodGet, "/v1/changes/repos/acme/widgets/pulls/99", nil, []string{"owner", "repo", "number"}, []string{"acme", "widgets", "99"})
	_, _, err = server.loadSnapshot(c)
	require.Equal(t, http.StatusNotFound, httpErrorCode(t, err))
}

func TestChangeSearchSuccessAndHelperBranches(t *testing.T) {
	t.Parallel()

	db := openHTTPAPITestDB(t)
	seedStoredGitHubReadData(t, db)
	seedChangeSearchSupportData(t, db)
	server := NewServer(db, Options{ChangeStatus: stubChangeStatus{repoStatus: gitindex.RepoStatus{FullName: "acme/widgets"}}})

	req := httptest.NewRequest(http.MethodGet, "/v1/changes/repos/acme/widgets/commits/head", nil)
	rec := httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	req = httptest.NewRequest(http.MethodGet, "/v1/changes/repos/acme/widgets/commits/head/files", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	req = httptest.NewRequest(http.MethodGet, "/v1/changes/repos/acme/widgets/compare/main...head", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	req = httptest.NewRequest(http.MethodGet, "/v1/search/repos/acme/widgets/status", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	req = httptest.NewRequest(http.MethodPost, "/v1/search/repos/acme/widgets/mentions", bytes.NewBufferString(`{"query":"watchdog","author":"octocat"}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var mentions []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &mentions))
	require.NotEmpty(t, mentions)

	req = httptest.NewRequest(http.MethodGet, "/v1/search/repos/acme/widgets/pulls/7/related?mode=range_overlap&state=all&limit=1", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	req = httptest.NewRequest(http.MethodGet, "/v1/search/repos/acme/widgets/pulls/7/related", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	req = httptest.NewRequest(http.MethodPost, "/v1/search/repos/acme/widgets/pulls/by-paths", bytes.NewBufferString(`{"paths":["app/service.go","app/service.go"],"state":"all"}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	req = httptest.NewRequest(http.MethodPost, "/v1/search/repos/acme/widgets/pulls/by-ranges", bytes.NewBufferString(`{"ranges":[{"path":"app/service.go","start":1,"end":2}],"state":"all"}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	req = httptest.NewRequest(http.MethodGet, "/v1/changes/repos/acme/widgets/compare/main...missing", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)

	req = httptest.NewRequest(http.MethodGet, "/v1/search/repos/missing/repo/status", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)

	req = httptest.NewRequest(http.MethodPost, "/v1/search/repos/missing/repo/mentions", bytes.NewBufferString(`{"query":"watchdog"}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)

	req = httptest.NewRequest(http.MethodGet, "/v1/changes/repos/missing/repo/commits/head", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)

	req = httptest.NewRequest(http.MethodGet, "/v1/changes/repos/acme/widgets/commits/missing", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)

	req = httptest.NewRequest(http.MethodGet, "/v1/changes/repos/missing/repo/commits/head/files", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)

	req = httptest.NewRequest(http.MethodGet, "/v1/changes/repos/acme/widgets/commits/missing/files", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)

	req = httptest.NewRequest(http.MethodPost, "/v1/search/repos/missing/repo/pulls/by-paths", bytes.NewBufferString(`{"paths":["app/service.go"]}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)

	req = httptest.NewRequest(http.MethodPost, "/v1/search/repos/missing/repo/pulls/by-ranges", bytes.NewBufferString(`{"ranges":[{"path":"app/service.go","start":1,"end":2}]}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)

	c, rec := newServerTestContext(server, http.MethodGet, "/v1/changes/repos/acme/widgets/commits/head", nil, []string{"owner", "repo", "sha"}, []string{"acme", "widgets", ""})
	require.NoError(t, server.handleGetCommit(c))
	require.Equal(t, http.StatusBadRequest, rec.Code)

	c, rec = newServerTestContext(server, http.MethodGet, "/v1/changes/repos/acme/widgets/commits/head/files", nil, []string{"owner", "repo", "sha"}, []string{"acme", "widgets", ""})
	require.NoError(t, server.handleListCommitFiles(c))
	require.Equal(t, http.StatusBadRequest, rec.Code)

	c, rec = newServerTestContext(server, http.MethodGet, "/v1/changes/repos/acme/widgets/compare/spec", nil, []string{"owner", "repo", "spec"}, []string{"acme", "widgets", "%zz...head"})
	require.NoError(t, server.handleCompareChanges(c))
	require.Equal(t, http.StatusBadRequest, rec.Code)

	c, _ = newServerTestContext(server, http.MethodGet, "/v1/changes/repos/missing/repo/compare/main...head", nil, []string{"owner", "repo", "spec"}, []string{"missing", "repo", "main...head"})
	err := server.handleCompareChanges(c)
	require.EqualError(t, err, "code=404, message=map[message:Not Found]")

	require.False(t, rangeOverlap(0, 0, 1, 1))
	require.False(t, rangeOverlap(1, 1, 0, 0))
	require.True(t, rangeOverlap(4, 3, 4, 4))
	require.Equal(t, 3, minNonZero(3, 7))
	require.Equal(t, 4, minNonZero(0, 4))
	require.True(t, noisyPath("web/vendor/mod.go"))
	require.True(t, noisyPath("ui/__snapshots__/view.snap"))
	require.True(t, noisyPath("package-lock.json"))
	require.False(t, noisyPath("app/service.go"))

	resolved, err := server.resolveGitRefOrSHA(context.Background(), 1, "main")
	require.NoError(t, err)
	require.Equal(t, "base", resolved)
	resolved, err = server.resolveGitRefOrSHA(context.Background(), 1, "unknown-ref")
	require.NoError(t, err)
	require.Equal(t, "unknown-ref", resolved)
}

func seedStoredGitHubReadData(t *testing.T, db *gorm.DB) {
	t.Helper()

	now := time.Now().UTC()
	repoRaw := []byte(`{"id":1,"full_name":"acme/widgets"}`)
	issueRaw := []byte(`{"id":7,"number":7,"title":"Issue 7","state":"open"}`)
	pullRaw := []byte(`{"id":17,"number":7,"title":"PR 7","state":"open"}`)
	commentRaw := []byte(`{"id":27,"body":"comment"}`)
	reviewRaw := []byte(`{"id":37,"state":"APPROVED"}`)
	reviewCommentRaw := []byte(`{"id":47,"body":"review comment"}`)

	repo := database.Repository{
		ID:            1,
		GitHubID:      1,
		OwnerLogin:    "acme",
		Name:          "widgets",
		FullName:      "acme/widgets",
		DefaultBranch: "main",
		Visibility:    "public",
		RawJSON:       repoRaw,
	}
	require.NoError(t, db.Create(&repo).Error)

	issue := database.Issue{
		ID:              1,
		RepositoryID:    repo.ID,
		GitHubID:        7,
		Number:          7,
		Title:           "Issue 7",
		State:           "open",
		CommentsCount:   1,
		IsPullRequest:   true,
		HTMLURL:         "https://github.com/acme/widgets/issues/7",
		APIURL:          "https://api.github.com/repos/acme/widgets/issues/7",
		GitHubCreatedAt: now,
		GitHubUpdatedAt: now,
		RawJSON:         issueRaw,
	}
	require.NoError(t, db.Create(&issue).Error)

	pull := database.PullRequest{
		IssueID:         issue.ID,
		RepositoryID:    repo.ID,
		GitHubID:        17,
		Number:          7,
		State:           "open",
		HeadRef:         "feature",
		HeadSHA:         "head",
		BaseRef:         "main",
		BaseSHA:         "base",
		HTMLURL:         "https://github.com/acme/widgets/pull/7",
		APIURL:          "https://api.github.com/repos/acme/widgets/pulls/7",
		GitHubCreatedAt: now,
		GitHubUpdatedAt: now,
		RawJSON:         pullRaw,
	}
	require.NoError(t, db.Create(&pull).Error)

	require.NoError(t, db.Create(&database.IssueComment{
		GitHubID:        27,
		RepositoryID:    repo.ID,
		IssueID:         issue.ID,
		Body:            "comment",
		HTMLURL:         "https://github.com/acme/widgets/issues/7#issuecomment-27",
		APIURL:          "https://api.github.com/repos/acme/widgets/issues/comments/27",
		GitHubCreatedAt: now,
		GitHubUpdatedAt: now,
		RawJSON:         commentRaw,
	}).Error)
	require.NoError(t, db.Create(&database.PullRequestReview{
		GitHubID:        37,
		RepositoryID:    repo.ID,
		PullRequestID:   issue.ID,
		State:           "APPROVED",
		HTMLURL:         "https://github.com/acme/widgets/pull/7#pullrequestreview-37",
		APIURL:          "https://api.github.com/repos/acme/widgets/pulls/reviews/37",
		GitHubCreatedAt: now,
		GitHubUpdatedAt: now,
		RawJSON:         reviewRaw,
	}).Error)
	require.NoError(t, db.Create(&database.PullRequestReviewComment{
		GitHubID:        47,
		RepositoryID:    repo.ID,
		PullRequestID:   issue.ID,
		Path:            "app/service.go",
		Body:            "review comment",
		HTMLURL:         "https://github.com/acme/widgets/pull/7#discussion_r47",
		APIURL:          "https://api.github.com/repos/acme/widgets/pulls/comments/47",
		PullRequestURL:  pull.APIURL,
		GitHubCreatedAt: now,
		GitHubUpdatedAt: now,
		RawJSON:         reviewCommentRaw,
	}).Error)

	snapshot := database.PullRequestChangeSnapshot{
		ID:                1,
		RepositoryID:      repo.ID,
		PullRequestID:     issue.ID,
		PullRequestNumber: 7,
		HeadSHA:           "head",
		BaseSHA:           "base",
		MergeBaseSHA:      "merge",
		BaseRef:           "main",
		State:             "open",
		IndexedAs:         "full",
		IndexFreshness:    "current",
		PathCount:         1,
		IndexedFileCount:  1,
		HunkCount:         1,
		Additions:         3,
		Deletions:         1,
		PatchBytes:        42,
		LastIndexedAt:     &now,
	}
	require.NoError(t, db.Create(&snapshot).Error)
	require.NoError(t, db.Create(&database.PullRequestChangeFile{
		SnapshotID:        snapshot.ID,
		RepositoryID:      repo.ID,
		PullRequestNumber: 7,
		HeadSHA:           "head",
		BaseSHA:           "base",
		MergeBaseSHA:      "merge",
		Path:              "app/service.go",
		Status:            "modified",
		FileKind:          "text",
		IndexedAs:         "full",
		PatchText:         "@@",
	}).Error)
	require.NoError(t, db.Create(&database.PullRequestChangeHunk{
		SnapshotID:        snapshot.ID,
		RepositoryID:      repo.ID,
		PullRequestNumber: 7,
		HeadSHA:           "head",
		BaseSHA:           "base",
		MergeBaseSHA:      "merge",
		Path:              "app/service.go",
		HunkIndex:         0,
		DiffHunk:          "@@",
	}).Error)
}

func seedChangeSearchSupportData(t *testing.T, db *gorm.DB) {
	t.Helper()

	now := time.Now().UTC()
	require.NoError(t, db.Model(&database.PullRequestChangeHunk{}).
		Where("snapshot_id = ? AND repository_id = ?", 1, 1).
		Updates(map[string]any{
			"old_start": 1,
			"old_count": 1,
			"old_end":   1,
			"new_start": 1,
			"new_count": 2,
			"new_end":   2,
		}).Error)

	require.NoError(t, db.Create(&database.PullRequestChangeSnapshot{
		ID:                2,
		RepositoryID:      1,
		PullRequestID:     1,
		PullRequestNumber: 8,
		HeadSHA:           "head-8",
		BaseSHA:           "base",
		MergeBaseSHA:      "merge",
		BaseRef:           "main",
		State:             "open",
		IndexedAs:         "full",
		IndexFreshness:    "current",
		PathCount:         1,
		IndexedFileCount:  1,
		HunkCount:         1,
	}).Error)
	require.NoError(t, db.Create(&database.PullRequestChangeFile{
		SnapshotID:        2,
		RepositoryID:      1,
		PullRequestNumber: 8,
		HeadSHA:           "head-8",
		BaseSHA:           "base",
		MergeBaseSHA:      "merge",
		Path:              "app/service.go",
		Status:            "modified",
		FileKind:          "text",
		IndexedAs:         "full",
		PatchText:         "@@",
	}).Error)
	require.NoError(t, db.Create(&database.PullRequestChangeHunk{
		SnapshotID:        2,
		RepositoryID:      1,
		PullRequestNumber: 8,
		HeadSHA:           "head-8",
		BaseSHA:           "base",
		MergeBaseSHA:      "merge",
		Path:              "app/service.go",
		HunkIndex:         0,
		DiffHunk:          "@@",
		OldStart:          1,
		OldCount:          1,
		OldEnd:            1,
		NewStart:          1,
		NewCount:          2,
		NewEnd:            2,
	}).Error)

	require.NoError(t, db.Create(&database.GitRef{
		RepositoryID:    1,
		RefName:         "refs/heads/main",
		TargetOID:       "base",
		PeeledCommitSHA: "base",
	}).Error)
	require.NoError(t, db.Create(&database.GitCommit{
		RepositoryID:   1,
		SHA:            "head",
		TreeSHA:        "tree",
		AuthorName:     "octocat",
		AuthorEmail:    "octocat@example.com",
		AuthoredAt:     now,
		CommitterName:  "octocat",
		CommitterEmail: "octocat@example.com",
		CommittedAt:    now,
		Message:        "commit",
	}).Error)
	require.NoError(t, db.Create(&database.GitCommitParent{
		RepositoryID:  1,
		CommitSHA:     "head",
		ParentSHA:     "base",
		ParentIndex:   0,
		IndexedAs:     "full",
		PathCount:     1,
		HunkCount:     1,
		Additions:     2,
		Deletions:     1,
		PatchBytes:    8,
		LastIndexedAt: &now,
	}).Error)
	require.NoError(t, db.Create(&database.GitCommitParentFile{
		RepositoryID: 1,
		CommitSHA:    "head",
		ParentSHA:    "base",
		ParentIndex:  0,
		Path:         "app/service.go",
		Status:       "modified",
		FileKind:     "text",
		IndexedAs:    "full",
		PatchText:    "@@",
	}).Error)
	require.NoError(t, db.Create(&database.GitCommitParentHunk{
		RepositoryID: 1,
		CommitSHA:    "head",
		ParentSHA:    "base",
		ParentIndex:  0,
		Path:         "app/service.go",
		HunkIndex:    0,
		DiffHunk:     "@@",
		OldStart:     1,
		OldCount:     1,
		OldEnd:       1,
		NewStart:     1,
		NewCount:     2,
		NewEnd:       2,
	}).Error)

	require.NoError(t, db.Create(&database.SearchDocument{
		RepositoryID:     1,
		DocumentType:     "pull_request",
		DocumentGitHubID: 17,
		Number:           7,
		State:            "open",
		AuthorLogin:      "octocat",
		APIURL:           "https://api.github.com/repos/acme/widgets/pulls/7",
		HTMLURL:          "https://github.com/acme/widgets/pull/7",
		TitleText:        "Watchdog PR 7",
		BodyText:         "mentions service",
		SearchText:       "Watchdog PR 7 mentions service",
		NormalizedText:   "watchdog pr 7 mentions service",
		ObjectCreatedAt:  now,
		ObjectUpdatedAt:  now,
	}).Error)
	require.NoError(t, db.Create(&database.RepoTextSearchState{
		RepositoryID:       1,
		Status:             "ready",
		Freshness:          "current",
		Coverage:           "complete",
		LastIndexedAt:      &now,
		LastSourceUpdateAt: &now,
	}).Error)
}
