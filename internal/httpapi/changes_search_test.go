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
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/dutifuldev/ghreplica/internal/github"
	"github.com/dutifuldev/ghreplica/internal/githubsync"
	"github.com/dutifuldev/ghreplica/internal/gitindex"
	"github.com/dutifuldev/ghreplica/internal/httpapi"
	"github.com/dutifuldev/ghreplica/internal/searchindex"
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

func TestChangeStatusEndpoints(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	fixture := testfixtures.CreateLocalPullRepo(t)
	repo, pulls := seedRepositoryAndPullRequests(t, db, fixture)
	indexer := gitindex.NewService(db, github.NewClient("https://api.github.com", github.AuthConfig{}), filepath.Join(t.TempDir(), "mirrors"))
	require.NoError(t, indexer.IndexPullRequest(ctx, "acme", "widgets", repo, pulls[101]))

	now := time.Now().UTC()
	cursorNumber := 102
	cursorUpdatedAt := now.Add(-time.Minute)
	fetchHeartbeat := now.Add(-15 * time.Second)
	fetchExpires := now.Add(45 * time.Second)
	backfillHeartbeat := now.Add(-10 * time.Second)
	backfillExpires := now.Add(50 * time.Second)
	require.NoError(t, db.Create(&database.RepoChangeSyncState{
		RepositoryID:             repo.ID,
		Dirty:                    true,
		DirtySince:               &now,
		LastWebhookAt:            &now,
		LastRequestedFetchAt:     &now,
		LastFetchStartedAt:       &now,
		LastFetchFinishedAt:      &now,
		LastSuccessfulFetchAt:    &now,
		LastBackfillStartedAt:    &now,
		LastBackfillFinishedAt:   &now,
		LastOpenPRScanAt:         &now,
		OpenPRTotal:              3,
		OpenPRCurrent:            1,
		OpenPRStale:              1,
		OpenPRCursorNumber:       &cursorNumber,
		OpenPRCursorUpdatedAt:    &cursorUpdatedAt,
		BackfillMode:             "open_only",
		BackfillPriority:         5,
		FetchLeaseOwnerID:        "worker-a",
		FetchLeaseHeartbeatAt:    &fetchHeartbeat,
		FetchLeaseUntil:          &fetchExpires,
		BackfillLeaseOwnerID:     "worker-a",
		BackfillLeaseHeartbeatAt: &backfillHeartbeat,
		BackfillLeaseUntil:       &backfillExpires,
	}).Error)

	service := githubsync.NewService(db, github.NewClient("https://api.github.com", github.AuthConfig{}), indexer)
	server := httpapi.NewServer(db, httpapi.Options{ChangeStatus: service})

	req := httptest.NewRequest(http.MethodGet, "/v1/changes/repos/acme/widgets/status", nil)
	rec := httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var repoStatus map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &repoStatus))
	require.Equal(t, "acme/widgets", repoStatus["full_name"])
	require.EqualValues(t, 3, repoStatus["open_pr_total"])
	require.EqualValues(t, 1, repoStatus["open_pr_current"])
	require.EqualValues(t, 1, repoStatus["open_pr_stale"])
	require.EqualValues(t, 1, repoStatus["open_pr_missing"])
	require.Equal(t, "open_only", repoStatus["backfill_mode"])
	require.Equal(t, "worker-a", repoStatus["fetch_lease_owner_id"])
	require.Equal(t, "worker-a", repoStatus["backfill_lease_owner_id"])

	req = httptest.NewRequest(http.MethodGet, "/v1/changes/repos/acme/widgets/pulls/101/status", nil)
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var prStatus map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &prStatus))
	require.EqualValues(t, 101, prStatus["pull_request_number"])
	require.Equal(t, true, prStatus["indexed"])
	require.Equal(t, "current", prStatus["index_freshness"])
	require.EqualValues(t, 2, prStatus["changed_files"])
}

func TestSearchMentionsEndpoint(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	repo := seedMentionSearchData(t, db)
	require.NoError(t, searchindex.NewService(db).RebuildRepositoryByID(ctx, repo.ID))

	server := httpapi.NewServer(db, httpapi.Options{})

	body := bytes.NewBufferString(`{"query":"heartbeat watchdog","mode":"fts","limit":10,"page":1}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/search/repos/acme/widgets/mentions", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var matches []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &matches))
	require.NotEmpty(t, matches)
	require.Equal(t, "pull_request", matches[0]["resource"].(map[string]any)["type"])
	require.Equal(t, "title", matches[0]["matched_field"])

	body = bytes.NewBufferString(`{"query":"watch dog","mode":"fuzzy","scopes":["pull_requests"],"limit":10,"page":1}`)
	req = httptest.NewRequest(http.MethodPost, "/v1/search/repos/acme/widgets/mentions", body)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &matches))
	require.Len(t, matches, 1)
	require.Equal(t, "pull_request", matches[0]["resource"].(map[string]any)["type"])

	body = bytes.NewBufferString(`{"query":"watchdog.*variable","mode":"regex","scopes":["pull_request_review_comments"],"limit":10,"page":1}`)
	req = httptest.NewRequest(http.MethodPost, "/v1/search/repos/acme/widgets/mentions", body)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &matches))
	require.Len(t, matches, 1)
	require.Equal(t, "pull_request_review_comment", matches[0]["resource"].(map[string]any)["type"])

	body = bytes.NewBufferString(`{"query":"(","mode":"regex"}`)
	req = httptest.NewRequest(http.MethodPost, "/v1/search/repos/acme/widgets/mentions", body)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func seedMentionSearchData(t *testing.T, db *gorm.DB) database.Repository {
	t.Helper()

	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	owner := database.User{GitHubID: 1, Login: "acme", Type: "Organization"}
	author := database.User{GitHubID: 2, Login: "octocat", Type: "User"}
	reviewer := database.User{GitHubID: 3, Login: "reviewer", Type: "User"}
	require.NoError(t, db.Create(&owner).Error)
	require.NoError(t, db.Create(&author).Error)
	require.NoError(t, db.Create(&reviewer).Error)

	repo := database.Repository{
		GitHubID:      101,
		OwnerID:       &owner.ID,
		OwnerLogin:    "acme",
		Name:          "widgets",
		FullName:      "acme/widgets",
		DefaultBranch: "main",
		APIURL:        "https://api.github.com/repos/acme/widgets",
		HTMLURL:       "https://github.com/acme/widgets",
	}
	require.NoError(t, db.Create(&repo).Error)

	issue := database.Issue{
		RepositoryID:    repo.ID,
		GitHubID:        201,
		Number:          1,
		Title:           "Heartbeat watchdog drops ACP messages",
		Body:            "The heartbeat watchdog silently drops ACP messages on reconnect.",
		State:           "open",
		AuthorID:        &author.ID,
		HTMLURL:         "https://github.com/acme/widgets/issues/1",
		APIURL:          "https://api.github.com/repos/acme/widgets/issues/1",
		GitHubCreatedAt: now,
		GitHubUpdatedAt: now,
	}
	require.NoError(t, db.Create(&issue).Error)

	prIssue := database.Issue{
		RepositoryID:      repo.ID,
		GitHubID:          301,
		Number:            10,
		Title:             "feat(acp): retry heartbeat watchdog",
		Body:              "Add heartbeat retry logic for ACP sessions.",
		State:             "open",
		AuthorID:          &author.ID,
		IsPullRequest:     true,
		PullRequestAPIURL: "https://api.github.com/repos/acme/widgets/pulls/10",
		HTMLURL:           "https://github.com/acme/widgets/pull/10",
		APIURL:            "https://api.github.com/repos/acme/widgets/issues/10",
		GitHubCreatedAt:   now.Add(1 * time.Minute),
		GitHubUpdatedAt:   now.Add(1 * time.Minute),
	}
	require.NoError(t, db.Create(&prIssue).Error)

	pr := database.PullRequest{
		IssueID:         prIssue.ID,
		RepositoryID:    repo.ID,
		GitHubID:        302,
		Number:          10,
		State:           "open",
		HeadRef:         "feat/watchdog",
		HeadSHA:         "abc123",
		BaseRef:         "main",
		BaseSHA:         "def456",
		HTMLURL:         "https://github.com/acme/widgets/pull/10",
		APIURL:          "https://api.github.com/repos/acme/widgets/pulls/10",
		DiffURL:         "https://github.com/acme/widgets/pull/10.diff",
		PatchURL:        "https://github.com/acme/widgets/pull/10.patch",
		GitHubCreatedAt: now.Add(1 * time.Minute),
		GitHubUpdatedAt: now.Add(2 * time.Minute),
	}
	require.NoError(t, db.Create(&pr).Error)

	reviewComment := database.PullRequestReviewComment{
		GitHubID:        601,
		RepositoryID:    repo.ID,
		PullRequestID:   pr.IssueID,
		AuthorID:        &reviewer.ID,
		Path:            "worker.go",
		Body:            "Please rename the watchdog variable before merge.",
		HTMLURL:         "https://github.com/acme/widgets/pull/10#discussion_r601",
		APIURL:          "https://api.github.com/repos/acme/widgets/pulls/comments/601",
		PullRequestURL:  "https://api.github.com/repos/acme/widgets/pulls/10",
		GitHubCreatedAt: now.Add(3 * time.Minute),
		GitHubUpdatedAt: now.Add(3 * time.Minute),
	}
	require.NoError(t, db.Create(&reviewComment).Error)
	return repo
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
