package githubsync_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/dutifuldev/ghreplica/internal/github"
	"github.com/dutifuldev/ghreplica/internal/githubsync"
	"github.com/dutifuldev/ghreplica/internal/gitindex"
	"github.com/dutifuldev/ghreplica/internal/testfixtures"
	"github.com/stretchr/testify/require"
)

func TestChangeSyncWorkerBackfillsOpenPullRequestsGradually(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open("sqlite://" + filepath.Join(t.TempDir(), "change-sync.db"))
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	fixture := testfixtures.CreateLocalPullRepo(t)
	server := newBackfillGitHubServer(t, fixture)
	defer server.Close()

	client := github.NewClient(server.URL, github.AuthConfig{})
	indexer := gitindex.NewService(db, client, filepath.Join(t.TempDir(), "mirrors"))
	service := githubsync.NewService(db, client, indexer)

	state, err := service.ConfigureRepoBackfill(ctx, "acme", "widgets", "open_only", 5)
	require.NoError(t, err)
	require.True(t, state.Dirty)
	require.Equal(t, "open_only", state.BackfillMode)

	worker := githubsync.NewChangeSyncWorker(
		db,
		service,
		time.Millisecond,
		time.Nanosecond,
		time.Nanosecond,
		time.Nanosecond,
		time.Second,
		time.Minute,
		1,
	)

	processed, err := worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)

	status, err := service.GetRepoChangeStatus(ctx, "acme", "widgets")
	require.NoError(t, err)
	require.Equal(t, 3, status.OpenPRTotal)
	require.Equal(t, 0, status.OpenPRCurrent)
	require.Equal(t, 3, status.OpenPRMissing)
	require.False(t, status.Dirty)
	require.Nil(t, status.OpenPRCursorNumber)
	require.Equal(t, 1, server.ListPullCount())

	var inventoryRows int64
	require.NoError(t, db.WithContext(ctx).Model(&database.RepoOpenPullInventory{}).Count(&inventoryRows).Error)
	require.EqualValues(t, 3, inventoryRows)

	processed, err = worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)

	status, err = service.GetRepoChangeStatus(ctx, "acme", "widgets")
	require.NoError(t, err)
	require.Equal(t, 1, status.OpenPRCurrent)
	require.Equal(t, 2, status.OpenPRMissing)
	require.False(t, status.Dirty)
	require.NotNil(t, status.OpenPRCursorNumber)
	require.Equal(t, 1, server.ListPullCount())

	processed, err = worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)

	status, err = service.GetRepoChangeStatus(ctx, "acme", "widgets")
	require.NoError(t, err)
	require.Equal(t, 2, status.OpenPRCurrent)
	require.Equal(t, 1, status.OpenPRMissing)
	require.False(t, status.Dirty)
	require.Equal(t, 1, server.ListPullCount())

	processed, err = worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)

	status, err = service.GetRepoChangeStatus(ctx, "acme", "widgets")
	require.NoError(t, err)
	require.Equal(t, 3, status.OpenPRCurrent)
	require.Equal(t, 0, status.OpenPRMissing)
	require.False(t, status.Dirty)
	require.Nil(t, status.OpenPRCursorNumber)
	require.Equal(t, 1, server.ListPullCount())

	prStatus, err := service.GetPullRequestChangeStatus(ctx, "acme", "widgets", 101)
	require.NoError(t, err)
	require.True(t, prStatus.Indexed)
	require.Equal(t, "current", prStatus.IndexFreshness)
	require.NotEmpty(t, prStatus.HeadSHA)

	var snapshots int64
	require.NoError(t, db.WithContext(ctx).Model(&database.PullRequestChangeSnapshot{}).Count(&snapshots).Error)
	require.EqualValues(t, 3, snapshots)
}

func TestChangeSyncWorkerBackfillsWhileRepoRemainsDirty(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open("sqlite://" + filepath.Join(t.TempDir(), "change-sync.db"))
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	fixture := testfixtures.CreateLocalPullRepo(t)
	server := newBackfillGitHubServer(t, fixture)
	defer server.Close()

	client := github.NewClient(server.URL, github.AuthConfig{})
	indexer := gitindex.NewService(db, client, filepath.Join(t.TempDir(), "mirrors"))
	service := githubsync.NewService(db, client, indexer)

	state, err := service.ConfigureRepoBackfill(ctx, "acme", "widgets", "open_only", 5)
	require.NoError(t, err)

	worker := githubsync.NewChangeSyncWorker(
		db,
		service,
		time.Millisecond,
		time.Nanosecond,
		time.Hour,
		time.Nanosecond,
		time.Second,
		time.Minute,
		1,
	)

	processed, err := worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)

	status, err := service.GetRepoChangeStatus(ctx, "acme", "widgets")
	require.NoError(t, err)
	firstFetchStarted := status.LastFetchStartedAt
	require.NotNil(t, firstFetchStarted)
	require.Equal(t, 1, server.ListPullCount())

	processed, err = worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)

	status, err = service.GetRepoChangeStatus(ctx, "acme", "widgets")
	require.NoError(t, err)
	require.Equal(t, 1, status.OpenPRCurrent)
	require.Equal(t, 1, server.ListPullCount())

	dirtyAt := time.Now().UTC()
	require.NoError(t, service.MarkRepositoryChangeDirty(ctx, state.RepositoryID, dirtyAt))

	processed, err = worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)

	status, err = service.GetRepoChangeStatus(ctx, "acme", "widgets")
	require.NoError(t, err)
	require.True(t, status.Dirty)
	require.Equal(t, 2, status.OpenPRCurrent)
	require.Equal(t, 1, status.OpenPRMissing)
	require.Equal(t, firstFetchStarted, status.LastFetchStartedAt)
	require.Equal(t, 1, server.ListPullCount())
}

type backfillGitHubServer struct {
	*httptest.Server
	mu            sync.Mutex
	listPullCount int
}

func (s *backfillGitHubServer) recordListPull() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listPullCount++
}

func (s *backfillGitHubServer) ListPullCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listPullCount
}

func newBackfillGitHubServer(t *testing.T, fixture testfixtures.LocalPullRepo) *backfillGitHubServer {
	t.Helper()

	repo := github.RepositoryResponse{
		ID:            101,
		NodeID:        "R_repo",
		Name:          "widgets",
		FullName:      "acme/widgets",
		HTMLURL:       fixture.RemoteURL,
		URL:           "https://api.github.test/repos/acme/widgets",
		DefaultBranch: "main",
		Visibility:    "public",
		Owner: &github.UserResponse{
			ID:     1,
			NodeID: "U_org",
			Login:  "acme",
			Type:   "Organization",
		},
		CreatedAt: time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC),
	}

	baseRepo := github.PullBranchRepository{
		ID:            repo.ID,
		NodeID:        repo.NodeID,
		Name:          repo.Name,
		FullName:      repo.FullName,
		Private:       repo.Private,
		Owner:         repo.Owner,
		HTMLURL:       repo.HTMLURL,
		Description:   repo.Description,
		Fork:          repo.Fork,
		URL:           repo.URL,
		DefaultBranch: repo.DefaultBranch,
		Visibility:    repo.Visibility,
		Archived:      repo.Archived,
		Disabled:      repo.Disabled,
		CreatedAt:     repo.CreatedAt,
		UpdatedAt:     repo.UpdatedAt,
	}

	pulls := map[int]github.PullRequestResponse{}
	order := []int{101, 102, 103}
	for i, number := range order {
		ref := fixture.Pulls[number]
		pulls[number] = github.PullRequestResponse{
			ID:           int64(2000 + number),
			NodeID:       "PR_" + strconv.Itoa(number),
			Number:       number,
			State:        "open",
			Title:        "PR " + strconv.Itoa(number),
			Body:         "test pull request",
			User:         &github.UserResponse{ID: 10 + int64(number), NodeID: "U_" + strconv.Itoa(number), Login: "user" + strconv.Itoa(number), Type: "User"},
			Draft:        false,
			Head:         github.PullBranch{Ref: ref.HeadRef, SHA: ref.HeadSHA, Repo: &baseRepo},
			Base:         github.PullBranch{Ref: "main", SHA: fixture.BaseSHA, Repo: &baseRepo},
			ChangedFiles: 2,
			Commits:      1,
			HTMLURL:      "https://github.com/acme/widgets/pull/" + strconv.Itoa(number),
			URL:          "https://api.github.test/repos/acme/widgets/pulls/" + strconv.Itoa(number),
			CreatedAt:    time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC),
			UpdatedAt:    time.Date(2026, 4, 15, 10, i, 0, 0, time.UTC),
		}
	}

	issues := map[int]github.IssueResponse{}
	for _, number := range order {
		pull := pulls[number]
		issues[number] = github.IssueResponse{
			ID:          int64(1000 + number),
			NodeID:      "I_" + strconv.Itoa(number),
			Number:      number,
			Title:       pull.Title,
			Body:        pull.Body,
			State:       pull.State,
			User:        pull.User,
			PullRequest: &github.IssuePullRequestRef{URL: pull.URL},
			HTMLURL:     "https://github.com/acme/widgets/issues/" + strconv.Itoa(number),
			URL:         "https://api.github.test/repos/acme/widgets/issues/" + strconv.Itoa(number),
			CreatedAt:   pull.CreatedAt,
			UpdatedAt:   pull.UpdatedAt,
		}
	}

	server := &backfillGitHubServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/widgets", func(w http.ResponseWriter, r *http.Request) {
		writeBackfillJSON(t, w, repo)
	})
	mux.HandleFunc("/repos/acme/widgets/pulls", func(w http.ResponseWriter, r *http.Request) {
		server.recordListPull()
		writeBackfillJSON(t, w, []github.PullRequestResponse{pulls[103], pulls[102], pulls[101]})
	})
	mux.HandleFunc("/repos/acme/widgets/issues/", func(w http.ResponseWriter, r *http.Request) {
		number, ok := tailNumber(r.URL.Path, "/repos/acme/widgets/issues/")
		require.True(t, ok)
		writeBackfillJSON(t, w, issues[number])
	})
	mux.HandleFunc("/repos/acme/widgets/pulls/", func(w http.ResponseWriter, r *http.Request) {
		number, ok := tailNumber(r.URL.Path, "/repos/acme/widgets/pulls/")
		require.True(t, ok)
		writeBackfillJSON(t, w, pulls[number])
	})

	server.Server = httptest.NewServer(mux)
	return server
}

func tailNumber(path, prefix string) (int, bool) {
	if !strings.HasPrefix(path, prefix) {
		return 0, false
	}
	rest := strings.TrimPrefix(path, prefix)
	if strings.Contains(rest, "/") {
		return 0, false
	}
	number, err := strconv.Atoi(rest)
	if err != nil {
		return 0, false
	}
	return number, true
}

func writeBackfillJSON(t *testing.T, w http.ResponseWriter, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(payload))
}
