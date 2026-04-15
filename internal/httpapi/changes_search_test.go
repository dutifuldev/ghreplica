package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/dutifuldev/ghreplica/internal/github"
	"github.com/dutifuldev/ghreplica/internal/gitindex"
	"github.com/dutifuldev/ghreplica/internal/httpapi"
	"github.com/dutifuldev/ghreplica/internal/testfixtures"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestChangeAndSearchEndpointsUseIndexedGitData(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	fixture := testfixtures.CreateLocalPullRepo(t)
	repo, pulls := seedRepositoryAndPullRequests(t, db, fixture)
	indexer := gitindex.NewService(db, github.NewClient("https://api.github.com", github.AuthConfig{}), filepath.Join(t.TempDir(), "mirrors"))
	for _, pull := range pulls {
		require.NoError(t, indexer.IndexPullRequest(ctx, "acme", "widgets", repo, pull))
	}
	extraSnapshot := database.PullRequestChangeSnapshot{
		RepositoryID:      repo.ID,
		PullRequestID:     pulls[103].IssueID,
		PullRequestNumber: 104,
		HeadSHA:           "paths-only-head",
		BaseSHA:           fixture.BaseSHA,
		MergeBaseSHA:      fixture.BaseSHA,
		BaseRef:           "main",
		State:             "open",
		IndexedAs:         "paths_only",
		IndexFreshness:    "current",
		PathCount:         1,
		IndexedFileCount:  1,
	}
	require.NoError(t, db.Create(&extraSnapshot).Error)
	require.NoError(t, db.Create(&database.PullRequestChangeFile{
		SnapshotID:        extraSnapshot.ID,
		RepositoryID:      repo.ID,
		PullRequestNumber: 104,
		HeadSHA:           "paths-only-head",
		BaseSHA:           fixture.BaseSHA,
		MergeBaseSHA:      fixture.BaseSHA,
		Path:              "app/service.go",
		Status:            "modified",
		FileKind:          "text",
		IndexedAs:         "paths_only",
	}).Error)

	server := httpapi.NewServer(db, httpapi.Options{})

	req := httptest.NewRequest(http.MethodGet, "/v1/changes/repos/acme/widgets/pulls/101", nil)
	rec := httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var snapshot map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &snapshot))
	require.Equal(t, "full", snapshot["indexed_as"])
	require.Equal(t, "current", snapshot["index_freshness"])

	req = httptest.NewRequest(http.MethodGet, "/v1/changes/repos/acme/widgets/pulls/101/files", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var prFiles []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &prFiles))
	require.Len(t, prFiles, 2)
	require.Equal(t, "app/service.go", prFiles[0]["path"])
	require.NotEmpty(t, prFiles[0]["hunks"])

	req = httptest.NewRequest(http.MethodGet, "/v1/changes/repos/acme/widgets/commits/"+pulls[101].HeadSHA, nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var commit map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &commit))
	require.Equal(t, pulls[101].HeadSHA, commit["sha"])
	require.Len(t, commit["parents"], 1)

	req = httptest.NewRequest(http.MethodGet, "/v1/changes/repos/acme/widgets/commits/"+pulls[101].HeadSHA+"/files", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var commitFiles []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &commitFiles))
	require.Len(t, commitFiles, 2)

	req = httptest.NewRequest(http.MethodGet, "/v1/changes/repos/acme/widgets/compare/main..."+pulls[101].HeadSHA, nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var compare map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &compare))
	require.Equal(t, "main", compare["base"])
	require.Equal(t, pulls[101].HeadSHA, compare["head"])
	require.Equal(t, fixture.BaseSHA, compare["resolved"].(map[string]any)["base"])
	require.Len(t, compare["files"], 2)

	escapedSnapshot := database.PullRequestChangeSnapshot{
		RepositoryID:      repo.ID,
		PullRequestID:     pulls[101].IssueID,
		PullRequestNumber: 105,
		HeadSHA:           pulls[101].HeadSHA,
		BaseSHA:           fixture.BaseSHA,
		MergeBaseSHA:      fixture.BaseSHA,
		BaseRef:           "release/2026.04",
		State:             "open",
		IndexedAs:         "full",
		IndexFreshness:    "current",
		PathCount:         1,
		IndexedFileCount:  1,
		HunkCount:         0,
	}
	require.NoError(t, db.Create(&escapedSnapshot).Error)
	require.NoError(t, db.Create(&database.PullRequestChangeFile{
		SnapshotID:        escapedSnapshot.ID,
		RepositoryID:      repo.ID,
		PullRequestNumber: 105,
		HeadSHA:           pulls[101].HeadSHA,
		BaseSHA:           fixture.BaseSHA,
		MergeBaseSHA:      fixture.BaseSHA,
		Path:              "app/service.go",
		Status:            "modified",
		FileKind:          "text",
		IndexedAs:         "full",
	}).Error)

	req = httptest.NewRequest(http.MethodGet, "/v1/changes/repos/acme/widgets/compare/release%2F2026.04..."+pulls[101].HeadSHA, nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &compare))
	require.Equal(t, "release/2026.04", compare["base"])
	require.EqualValues(t, 105, compare["snapshot"].(map[string]any)["pull_request_number"])
	require.Len(t, compare["files"], 1)

	req = httptest.NewRequest(http.MethodGet, "/v1/search/repos/acme/widgets/pulls/101/related?mode=path_overlap&state=all", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var related []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &related))
	require.Len(t, related, 3)
	require.ElementsMatch(t,
		[]any{float64(102), float64(104), float64(105)},
		[]any{related[0]["pull_request_number"], related[1]["pull_request_number"], related[2]["pull_request_number"]},
	)

	req = httptest.NewRequest(http.MethodGet, "/v1/search/repos/acme/widgets/pulls/101/related?mode=range_overlap&state=all", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &related))
	require.Len(t, related, 1)
	require.EqualValues(t, 102, related[0]["pull_request_number"])
	require.EqualValues(t, 1, related[0]["overlapping_hunks"])

	body := bytes.NewBufferString(`{"paths":["app/service.go"],"state":"all","limit":10}`)
	req = httptest.NewRequest(http.MethodPost, "/v1/search/repos/acme/widgets/pulls/by-paths", body)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var byPaths []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &byPaths))
	require.Len(t, byPaths, 4)
	require.ElementsMatch(t,
		[]any{float64(101), float64(102), float64(104), float64(105)},
		[]any{byPaths[0]["pull_request_number"], byPaths[1]["pull_request_number"], byPaths[2]["pull_request_number"], byPaths[3]["pull_request_number"]},
	)

	body = bytes.NewBufferString(`{"paths":["docs/readme.md"],"state":"all","limit":10}`)
	req = httptest.NewRequest(http.MethodPost, "/v1/search/repos/acme/widgets/pulls/by-paths", body)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &byPaths))
	require.Len(t, byPaths, 1)
	require.EqualValues(t, 103, byPaths[0]["pull_request_number"])

	body = bytes.NewBufferString(`{"ranges":[{"path":"app/service.go","start":4,"end":6}],"state":"all","limit":10}`)
	req = httptest.NewRequest(http.MethodPost, "/v1/search/repos/acme/widgets/pulls/by-ranges", body)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var byRanges []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &byRanges))
	require.Len(t, byRanges, 2)
	require.ElementsMatch(t, []any{float64(101), float64(102)}, []any{byRanges[0]["pull_request_number"], byRanges[1]["pull_request_number"]})
}

func seedRepositoryAndPullRequests(t *testing.T, db *gorm.DB, fixture testfixtures.LocalPullRepo) (database.Repository, map[int]database.PullRequest) {
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

	pulls := make(map[int]database.PullRequest, len(fixture.Pulls))
	for number, ref := range fixture.Pulls {
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
		pulls[number] = pull
	}

	return repo, pulls
}
