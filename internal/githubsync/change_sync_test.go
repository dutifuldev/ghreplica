package githubsync_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/dutifuldev/ghreplica/internal/github"
	"github.com/dutifuldev/ghreplica/internal/githubsync"
	"github.com/dutifuldev/ghreplica/internal/gitindex"
	"github.com/dutifuldev/ghreplica/internal/testfixtures"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestChangeSyncWorkerBackfillsOpenPullRequestsGradually(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open("sqlite://" + filepath.Join(t.TempDir(), "change-sync.db"))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

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
	require.False(t, status.InventoryNeedsRefresh)
	require.Equal(t, 1, status.InventoryGenerationCurrent)
	require.Nil(t, status.BackfillCursor)
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
	require.False(t, status.InventoryNeedsRefresh)
	require.NotNil(t, status.BackfillCursor)
	require.Equal(t, 1, server.ListPullCount())

	processed, err = worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)

	status, err = service.GetRepoChangeStatus(ctx, "acme", "widgets")
	require.NoError(t, err)
	require.Equal(t, 2, status.OpenPRCurrent)
	require.Equal(t, 1, status.OpenPRMissing)
	require.False(t, status.InventoryNeedsRefresh)
	require.Equal(t, 1, server.ListPullCount())

	processed, err = worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)

	status, err = service.GetRepoChangeStatus(ctx, "acme", "widgets")
	require.NoError(t, err)
	require.Equal(t, 3, status.OpenPRCurrent)
	require.Equal(t, 0, status.OpenPRMissing)
	require.False(t, status.InventoryNeedsRefresh)
	require.Nil(t, status.BackfillCursor)
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
	require.NoError(t, database.ApplyTestSchema(db))

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
		time.Hour,
		time.Minute,
		time.Second,
		time.Minute,
		1,
	)

	processed, err := worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)

	status, err := service.GetRepoChangeStatus(ctx, "acme", "widgets")
	require.NoError(t, err)
	firstInventoryStarted := status.LastInventoryScanStartedAt
	require.NotNil(t, firstInventoryStarted)
	require.Equal(t, 1, server.ListPullCount())

	processed, err = worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)

	status, err = service.GetRepoChangeStatus(ctx, "acme", "widgets")
	require.NoError(t, err)
	require.Equal(t, 1, status.OpenPRCurrent)
	require.Equal(t, 1, server.ListPullCount())

	dirtyAt := time.Now().UTC()
	require.NoError(t, service.MarkInventoryNeedsRefresh(ctx, state.RepositoryID, dirtyAt))
	status, err = service.GetRepoChangeStatus(ctx, "acme", "widgets")
	require.NoError(t, err)
	require.True(t, status.InventoryNeedsRefresh)

	processed, err = worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)

	status, err = service.GetRepoChangeStatus(ctx, "acme", "widgets")
	require.NoError(t, err)
	require.True(t, status.InventoryNeedsRefresh)
	require.Equal(t, 2, status.OpenPRCurrent)
	require.Equal(t, 1, status.OpenPRMissing)
	require.Equal(t, firstInventoryStarted, status.LastInventoryScanStartedAt)
	require.Equal(t, 1, server.ListPullCount())
}

func TestChangeSyncWorkerProcessesTargetedRefreshWithoutRescanningInventory(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open("sqlite://" + filepath.Join(t.TempDir(), "change-sync.db"))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

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
		time.Second,
		time.Minute,
		3,
	)

	processed, err := worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)
	require.Equal(t, 1, server.ListPullCount())

	require.NoError(t, service.EnqueuePullRequestRefresh(ctx, state.RepositoryID, 101, time.Now().UTC()))

	status, err := service.GetRepoChangeStatus(ctx, "acme", "widgets")
	require.NoError(t, err)
	require.True(t, status.TargetedRefreshPending)

	processed, err = worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)
	require.Equal(t, 1, server.ListPullCount())

	status, err = service.GetRepoChangeStatus(ctx, "acme", "widgets")
	require.NoError(t, err)
	require.False(t, status.TargetedRefreshPending)

	prStatus, err := service.GetPullRequestChangeStatus(ctx, "acme", "widgets", 101)
	require.NoError(t, err)
	require.True(t, prStatus.Indexed)
	require.Equal(t, "current", prStatus.IndexFreshness)
}

func TestChangeSyncWorkerDefersInventoryRefreshWhenSnapshotIsStillUsable(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open("sqlite://" + filepath.Join(t.TempDir(), "change-sync.db"))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

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
		time.Second,
		time.Minute,
		3,
	)

	processed, err := worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)
	require.Equal(t, 1, server.ListPullCount())

	seenAt := time.Now().UTC()
	require.NoError(t, service.EnqueuePullRequestRefresh(ctx, state.RepositoryID, 101, seenAt))
	require.NoError(t, service.MarkInventoryNeedsRefresh(ctx, state.RepositoryID, seenAt))

	status, err := service.GetRepoChangeStatus(ctx, "acme", "widgets")
	require.NoError(t, err)
	require.True(t, status.TargetedRefreshPending)

	processed, err = worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)
	require.Equal(t, 1, server.ListPullCount())

	status, err = service.GetRepoChangeStatus(ctx, "acme", "widgets")
	require.NoError(t, err)
	require.True(t, status.InventoryNeedsRefresh)
	require.False(t, status.TargetedRefreshPending)
}

func TestChangeSyncWorkerRefreshesInventoryWhenSnapshotIsOldEnough(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open("sqlite://" + filepath.Join(t.TempDir(), "change-sync.db"))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

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
		time.Second,
		time.Minute,
		1,
	)

	processed, err := worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)
	require.Equal(t, 1, server.ListPullCount())

	for i := 0; i < 3; i++ {
		processed, err = worker.RunOnce(ctx)
		require.NoError(t, err)
		require.True(t, processed)
	}
	require.Equal(t, 1, server.ListPullCount())

	oldScanAt := time.Now().UTC().Add(-2 * time.Hour)
	require.NoError(t, db.WithContext(ctx).Model(&database.RepoChangeSyncState{}).
		Where("id = ?", state.ID).
		Updates(map[string]any{
			"dirty":                       true,
			"dirty_since":                 oldScanAt,
			"last_requested_fetch_at":     oldScanAt,
			"inventory_last_committed_at": oldScanAt,
			"last_open_pr_scan_at":        oldScanAt,
		}).Error)

	processed, err = worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)
	require.Equal(t, 2, server.ListPullCount())

	status, err := service.GetRepoChangeStatus(ctx, "acme", "widgets")
	require.NoError(t, err)
	require.False(t, status.InventoryNeedsRefresh)
}

func TestChangeSyncWorkerTargetedRefreshRestoresMissingInventoryRow(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open("sqlite://" + filepath.Join(t.TempDir(), "change-sync.db"))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

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
		time.Second,
		time.Minute,
		1,
	)

	processed, err := worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)
	require.Equal(t, 1, server.ListPullCount())

	require.NoError(t, db.WithContext(ctx).
		Where("repository_id = ? AND generation = ? AND pull_request_number = ?", state.RepositoryID, 1, 101).
		Delete(&database.RepoOpenPullInventory{}).Error)
	require.NoError(t, db.WithContext(ctx).Model(&database.RepoChangeSyncState{}).
		Where("id = ?", state.ID).
		Updates(map[string]any{
			"open_pr_total": 2,
			"updated_at":    time.Now().UTC(),
		}).Error)

	seenAt := time.Now().UTC()
	require.NoError(t, service.EnqueuePullRequestRefresh(ctx, state.RepositoryID, 101, seenAt))
	require.NoError(t, service.MarkInventoryNeedsRefresh(ctx, state.RepositoryID, seenAt))

	processed, err = worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)
	require.Equal(t, 1, server.ListPullCount())

	status, err := service.GetRepoChangeStatus(ctx, "acme", "widgets")
	require.NoError(t, err)
	require.Equal(t, 3, status.OpenPRTotal)

	var restored database.RepoOpenPullInventory
	require.NoError(t, db.WithContext(ctx).
		Where("repository_id = ? AND generation = ? AND pull_request_number = ?", state.RepositoryID, status.InventoryGenerationCurrent, 101).
		First(&restored).Error)
	require.Equal(t, "current", restored.FreshnessState)
}

func TestMarkBaseRefStaleUpdatesCurrentInventoryGeneration(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open("sqlite://" + filepath.Join(t.TempDir(), "change-sync.db"))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	fixture := testfixtures.CreateLocalPullRepo(t)
	server := newBackfillGitHubServer(t, fixture)
	defer server.Close()

	client := github.NewClient(server.URL, github.AuthConfig{})
	indexer := gitindex.NewService(db, client, filepath.Join(t.TempDir(), "mirrors"))
	service := githubsync.NewService(db, client, indexer)

	_, err = service.ConfigureRepoBackfill(ctx, "acme", "widgets", "open_only", 5)
	require.NoError(t, err)

	worker := githubsync.NewChangeSyncWorker(
		db,
		service,
		time.Millisecond,
		time.Nanosecond,
		time.Hour,
		time.Second,
		time.Minute,
		1,
	)

	processed, err := worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)

	processed, err = worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)

	status, err := service.GetRepoChangeStatus(ctx, "acme", "widgets")
	require.NoError(t, err)
	require.Equal(t, 1, status.OpenPRCurrent)

	require.NoError(t, service.MarkBaseRefStale(ctx, status.RepositoryID, "refs/heads/main"))

	status, err = service.GetRepoChangeStatus(ctx, "acme", "widgets")
	require.NoError(t, err)
	require.Equal(t, 0, status.OpenPRCurrent)
	require.Equal(t, 3, status.OpenPRStale)

	var inventory database.RepoOpenPullInventory
	require.NoError(t, db.WithContext(ctx).
		Where("repository_id = ? AND generation = ? AND pull_request_number = ?", status.RepositoryID, status.InventoryGenerationCurrent, 101).
		First(&inventory).Error)
	require.Equal(t, "stale_base_moved", inventory.FreshnessState)
}

func TestChangeSyncWorkerPreservesNewInventoryRefreshRequestedDuringScan(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open("sqlite://" + filepath.Join(t.TempDir(), "change-sync.db"))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	fixture := testfixtures.CreateLocalPullRepo(t)
	server := newBackfillGitHubServer(t, fixture)
	defer server.Close()

	scanStarted := make(chan struct{})
	releaseScan := make(chan struct{})
	server.onListPull = func() {
		select {
		case <-scanStarted:
		default:
			close(scanStarted)
		}
		<-releaseScan
	}

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
		time.Second,
		time.Minute,
		1,
	)

	done := make(chan error, 1)
	go func() {
		_, runErr := worker.RunOnce(ctx)
		done <- runErr
	}()

	<-scanStarted
	require.NoError(t, service.MarkInventoryNeedsRefresh(ctx, state.RepositoryID, time.Now().UTC()))
	close(releaseScan)
	require.NoError(t, <-done)

	status, err := service.GetRepoChangeStatus(ctx, "acme", "widgets")
	require.NoError(t, err)
	require.True(t, status.InventoryNeedsRefresh)
	require.Equal(t, 1, server.ListPullCount())
}

func TestChangeSyncWorkerRunOnceReclaimsStaleFetchLease(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open("sqlite://" + filepath.Join(t.TempDir(), "change-sync.db"))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	fixture := testfixtures.CreateLocalPullRepo(t)
	server := newBackfillGitHubServer(t, fixture)
	defer server.Close()

	client := github.NewClient(server.URL, github.AuthConfig{})
	indexer := gitindex.NewService(db, client, filepath.Join(t.TempDir(), "mirrors"))
	service := githubsync.NewService(db, client, indexer)

	state, err := service.ConfigureRepoBackfill(ctx, "acme", "widgets", "open_only", 5)
	require.NoError(t, err)

	staleNow := time.Now().UTC()
	staleHeartbeat := staleNow.Add(-2 * time.Second)
	staleUntil := staleNow.Add(time.Hour)
	require.NoError(t, db.WithContext(ctx).Model(&database.RepoChangeSyncState{}).
		Where("id = ?", state.ID).
		Updates(map[string]any{
			"fetch_lease_owner_id":     "dead-worker",
			"fetch_lease_started_at":   staleHeartbeat,
			"fetch_lease_heartbeat_at": staleHeartbeat,
			"fetch_lease_until":        staleUntil,
		}).Error)

	worker := githubsync.NewChangeSyncWorker(
		db,
		service,
		time.Millisecond,
		time.Nanosecond,
		time.Nanosecond,
		time.Second,
		time.Minute,
		1,
	)

	processed, err := worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)
	require.Equal(t, 1, server.ListPullCount())

	var refreshed database.RepoChangeSyncState
	require.NoError(t, db.WithContext(ctx).Where("id = ?", state.ID).First(&refreshed).Error)
	require.Empty(t, refreshed.FetchLeaseOwnerID)
	require.Nil(t, refreshed.FetchLeaseHeartbeatAt)
	require.Nil(t, refreshed.FetchLeaseUntil)
	require.NotNil(t, refreshed.LastFetchFinishedAt)
}

func TestChangeSyncWorkerRunOnceDoesNotStealFreshFetchLease(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open("sqlite://" + filepath.Join(t.TempDir(), "change-sync.db"))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	fixture := testfixtures.CreateLocalPullRepo(t)
	server := newBackfillGitHubServer(t, fixture)
	defer server.Close()

	client := github.NewClient(server.URL, github.AuthConfig{})
	indexer := gitindex.NewService(db, client, filepath.Join(t.TempDir(), "mirrors"))
	service := githubsync.NewService(db, client, indexer)

	state, err := service.ConfigureRepoBackfill(ctx, "acme", "widgets", "open_only", 5)
	require.NoError(t, err)

	freshNow := time.Now().UTC()
	freshUntil := freshNow.Add(time.Minute)
	require.NoError(t, db.WithContext(ctx).Model(&database.RepoChangeSyncState{}).
		Where("id = ?", state.ID).
		Updates(map[string]any{
			"fetch_lease_owner_id":     "other-worker",
			"fetch_lease_started_at":   freshNow,
			"fetch_lease_heartbeat_at": freshNow,
			"fetch_lease_until":        freshUntil,
		}).Error)

	worker := githubsync.NewChangeSyncWorker(
		db,
		service,
		time.Millisecond,
		time.Nanosecond,
		time.Nanosecond,
		time.Second,
		time.Minute,
		1,
	)

	processed, err := worker.RunOnce(ctx)
	require.NoError(t, err)
	require.False(t, processed)
	require.Equal(t, 0, server.ListPullCount())
}

func TestChangeSyncWorkerStartRecoversStaleLeasesOnStartup(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open("sqlite://" + filepath.Join(t.TempDir(), "change-sync.db"))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	repo := database.Repository{
		GitHubID:      101,
		OwnerLogin:    "acme",
		Name:          "widgets",
		FullName:      "acme/widgets",
		HTMLURL:       "https://github.com/acme/widgets",
		APIURL:        "https://api.github.test/repos/acme/widgets",
		DefaultBranch: "main",
		Visibility:    "public",
	}
	require.NoError(t, db.WithContext(ctx).Create(&repo).Error)

	staleNow := time.Now().UTC()
	staleHeartbeat := staleNow.Add(-2 * time.Second)
	staleUntil := staleNow.Add(time.Hour)
	state := database.RepoChangeSyncState{
		RepositoryID:             repo.ID,
		BackfillMode:             "off",
		FetchLeaseOwnerID:        "old-worker",
		FetchLeaseStartedAt:      &staleHeartbeat,
		FetchLeaseHeartbeatAt:    &staleHeartbeat,
		FetchLeaseUntil:          &staleUntil,
		BackfillLeaseOwnerID:     "old-worker",
		BackfillLeaseStartedAt:   &staleHeartbeat,
		BackfillLeaseHeartbeatAt: &staleHeartbeat,
		BackfillLeaseUntil:       &staleUntil,
	}
	require.NoError(t, db.WithContext(ctx).Create(&state).Error)

	service := githubsync.NewService(db, github.NewClient("https://api.github.test", github.AuthConfig{}), nil)
	worker := githubsync.NewChangeSyncWorker(
		db,
		service,
		time.Hour,
		time.Nanosecond,
		time.Nanosecond,
		time.Second,
		time.Minute,
		1,
	)

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- worker.Start(runCtx)
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	require.NoError(t, <-done)

	var refreshed database.RepoChangeSyncState
	require.NoError(t, db.WithContext(ctx).Where("id = ?", state.ID).First(&refreshed).Error)
	require.Empty(t, refreshed.FetchLeaseOwnerID)
	require.Nil(t, refreshed.FetchLeaseHeartbeatAt)
	require.Nil(t, refreshed.FetchLeaseUntil)
	require.Empty(t, refreshed.BackfillLeaseOwnerID)
	require.Nil(t, refreshed.BackfillLeaseHeartbeatAt)
	require.Nil(t, refreshed.BackfillLeaseUntil)
}

func TestChangeSyncWorkerRecentPRRepairClosesStalePullRequest(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open("sqlite://" + filepath.Join(t.TempDir(), "change-sync.db"))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

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
		time.Second,
		time.Minute,
		1,
	)

	staleAt := time.Now().UTC().Add(-2 * time.Hour)
	seedStalePull(t, ctx, db, fixture, state.RepositoryID, 101, staleAt)

	repairAt := time.Now().UTC().Add(2 * time.Hour)
	server.SetPullState(101, "closed", repairAt)

	_, err = service.RequestRecentPRRepair(ctx, "acme", "widgets")
	require.NoError(t, err)

	processed, err := worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)

	prStatus, err := service.GetPullRequestChangeStatus(ctx, "acme", "widgets", 101)
	require.NoError(t, err)
	require.Equal(t, "closed", prStatus.State)

	status, err := service.GetRepoChangeStatus(ctx, "acme", "widgets")
	require.NoError(t, err)
	require.False(t, status.RecentPRRepairPending)
	require.NotNil(t, status.LastRecentPRRepairStartedAt)
	require.NotNil(t, status.LastRecentPRRepairFinishedAt)
	require.NotNil(t, status.LastSuccessfulRecentPRRepairAt)

	var pull database.PullRequest
	require.NoError(t, db.WithContext(ctx).
		Where("repository_id = ? AND number = ?", state.RepositoryID, 101).
		First(&pull).Error)
	require.Equal(t, "closed", pull.State)
}

func TestChangeSyncWorkerRecentPRRepairClosesStaleIssueWhenPullRowIsAlreadyFresh(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open("sqlite://" + filepath.Join(t.TempDir(), "change-sync.db"))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	fixture := testfixtures.CreateLocalPullRepo(t)
	server := newBackfillGitHubServer(t, fixture)
	defer server.Close()

	client := github.NewClient(server.URL, github.AuthConfig{})
	indexer := gitindex.NewService(db, client, filepath.Join(t.TempDir(), "mirrors"))
	service := githubsync.NewService(db, client, indexer)

	state, err := service.ConfigureRepoBackfill(ctx, "acme", "widgets", "open_only", 5)
	require.NoError(t, err)

	repairAt := time.Now().UTC().Add(2 * time.Hour)
	server.SetPullState(101, "closed", repairAt)
	for _, number := range []int{101, 102, 103} {
		seedServerPullState(t, ctx, service, state.RepositoryID, server, number)
	}

	staleAt := repairAt.Add(-time.Hour)
	require.NoError(t, db.WithContext(ctx).
		Model(&database.Issue{}).
		Where("repository_id = ? AND number = ?", state.RepositoryID, 101).
		Updates(map[string]any{
			"state":             "open",
			"github_updated_at": staleAt,
			"closed_at":         nil,
		}).Error)

	_, err = service.RequestRecentPRRepair(ctx, "acme", "widgets")
	require.NoError(t, err)

	worker := githubsync.NewChangeSyncWorker(
		db,
		service,
		time.Millisecond,
		time.Nanosecond,
		time.Hour,
		time.Second,
		time.Minute,
		1,
	)

	processed, err := worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)

	var issue database.Issue
	require.NoError(t, db.WithContext(ctx).
		Where("repository_id = ? AND number = ?", state.RepositoryID, 101).
		First(&issue).Error)
	require.Equal(t, "closed", issue.State)
	require.NotNil(t, issue.ClosedAt)
	require.Equal(t, 0, server.GetPullCount())
	require.GreaterOrEqual(t, server.GetIssueCount(), 1)
}

func TestChangeSyncWorkerRecentPRRepairSkipsUnchangedRows(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open("sqlite://" + filepath.Join(t.TempDir(), "change-sync.db"))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	fixture := testfixtures.CreateLocalPullRepo(t)
	server := newBackfillGitHubServer(t, fixture)
	defer server.Close()

	client := github.NewClient(server.URL, github.AuthConfig{})
	indexer := gitindex.NewService(db, client, filepath.Join(t.TempDir(), "mirrors"))
	service := githubsync.NewService(db, client, indexer)

	state, err := service.ConfigureRepoBackfill(ctx, "acme", "widgets", "open_only", 5)
	require.NoError(t, err)

	for _, number := range []int{101, 102, 103} {
		seedServerPullState(t, ctx, service, state.RepositoryID, server, number)
	}

	_, err = service.RequestRecentPRRepair(ctx, "acme", "widgets")
	require.NoError(t, err)

	worker := githubsync.NewChangeSyncWorker(
		db,
		service,
		time.Millisecond,
		time.Nanosecond,
		time.Hour,
		time.Second,
		time.Minute,
		1,
	)

	processed, err := worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)
	require.Equal(t, 0, server.GetPullCount())
	require.Equal(t, 0, server.GetIssueCount())
	require.GreaterOrEqual(t, server.ListPullCount(), 1)
	require.GreaterOrEqual(t, server.ListIssueCount(), 1)
}

func TestChangeSyncWorkerRecentPRRepairRunsBeforeTargetedRefreshBurst(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open("sqlite://" + filepath.Join(t.TempDir(), "change-sync.db"))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	fixture := testfixtures.CreateLocalPullRepo(t)
	server := newBackfillGitHubServer(t, fixture)
	defer server.Close()

	client := github.NewClient(server.URL, github.AuthConfig{})
	indexer := gitindex.NewService(db, client, filepath.Join(t.TempDir(), "mirrors"))
	service := githubsync.NewService(db, client, indexer)

	state, err := service.ConfigureRepoBackfill(ctx, "acme", "widgets", "open_only", 5)
	require.NoError(t, err)

	for _, number := range []int{101, 102, 103} {
		seedServerPullState(t, ctx, service, state.RepositoryID, server, number)
	}

	_, err = service.RequestRecentPRRepair(ctx, "acme", "widgets")
	require.NoError(t, err)
	require.NoError(t, service.EnqueuePullRequestRefresh(ctx, state.RepositoryID, 101, time.Now().UTC()))

	worker := githubsync.NewChangeSyncWorker(
		db,
		service,
		time.Millisecond,
		time.Nanosecond,
		time.Hour,
		time.Second,
		time.Minute,
		1,
	)

	processed, err := worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)
	require.Equal(t, 0, server.GetPullCount())
	require.Equal(t, 0, server.GetIssueCount())

	status, err := service.GetRepoChangeStatus(ctx, "acme", "widgets")
	require.NoError(t, err)
	require.False(t, status.RecentPRRepairPending)
	require.True(t, status.TargetedRefreshPending)
}

func TestChangeSyncWorkerRecentPRRepairAdvancesCursorAcrossPages(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open("sqlite://" + filepath.Join(t.TempDir(), "change-sync.db"))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	fixture := testfixtures.CreateLocalPullRepo(t)
	server := newBackfillGitHubServer(t, fixture)
	defer server.Close()

	client := github.NewClient(server.URL, github.AuthConfig{})
	indexer := gitindex.NewService(db, client, filepath.Join(t.TempDir(), "mirrors"))
	service := githubsync.NewService(db, client, indexer)

	state, err := service.ConfigureRepoBackfill(ctx, "acme", "widgets", "open_only", 5)
	require.NoError(t, err)

	staleAt := time.Now().UTC().Add(-2 * time.Hour)
	for _, number := range []int{101, 102, 103} {
		seedStalePull(t, ctx, db, fixture, state.RepositoryID, number, staleAt)
		server.SetPullState(number, "closed", time.Now().UTC().Add(2*time.Hour+time.Duration(number)*time.Minute))
	}

	_, err = service.RequestRecentPRRepair(ctx, "acme", "widgets")
	require.NoError(t, err)

	worker := githubsync.NewChangeSyncWorker(
		db,
		service,
		time.Millisecond,
		time.Nanosecond,
		time.Hour,
		time.Second,
		time.Minute,
		1,
	)
	workerRecentMaxPages(worker, 1, 1)

	processed, err := worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)

	status, err := service.GetRepoChangeStatus(ctx, "acme", "widgets")
	require.NoError(t, err)
	require.True(t, status.RecentPRRepairPending)

	var refreshed database.RepoChangeSyncState
	require.NoError(t, db.WithContext(ctx).Where("id = ?", state.ID).First(&refreshed).Error)
	require.Equal(t, 2, refreshed.RecentPRRepairCursorPage)

	var pr103 database.PullRequest
	require.NoError(t, db.WithContext(ctx).
		Where("repository_id = ? AND number = ?", state.RepositoryID, 103).
		First(&pr103).Error)
	require.Equal(t, "closed", pr103.State)

	var pr102 database.PullRequest
	require.NoError(t, db.WithContext(ctx).
		Where("repository_id = ? AND number = ?", state.RepositoryID, 102).
		First(&pr102).Error)
	require.Equal(t, "open", pr102.State)

	processed, err = worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)

	require.NoError(t, db.WithContext(ctx).Where("id = ?", state.ID).First(&refreshed).Error)
	require.Equal(t, 3, refreshed.RecentPRRepairCursorPage)

	require.NoError(t, db.WithContext(ctx).
		Where("repository_id = ? AND number = ?", state.RepositoryID, 102).
		First(&pr102).Error)
	require.Equal(t, "closed", pr102.State)
}

func TestChangeSyncWorkerFullHistoryModeHandsOffToFullHistoryAfterIncompleteRecentRepair(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open("sqlite://" + filepath.Join(t.TempDir(), "change-sync.db"))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	fixture := testfixtures.CreateLocalPullRepo(t)
	server := newBackfillGitHubServer(t, fixture)
	defer server.Close()

	client := github.NewClient(server.URL, github.AuthConfig{})
	indexer := gitindex.NewService(db, client, filepath.Join(t.TempDir(), "mirrors"))
	service := githubsync.NewService(db, client, indexer)

	state, err := service.ConfigureRepoBackfill(ctx, "acme", "widgets", "full_history", 5)
	require.NoError(t, err)

	staleAt := time.Now().UTC().Add(-2 * time.Hour)
	for _, number := range []int{101, 102, 103} {
		seedStalePull(t, ctx, db, fixture, state.RepositoryID, number, staleAt)
		server.SetPullState(number, "closed", time.Now().UTC().Add(2*time.Hour+time.Duration(number)*time.Minute))
	}

	_, err = service.RequestRecentPRRepair(ctx, "acme", "widgets")
	require.NoError(t, err)

	worker := githubsync.NewChangeSyncWorker(
		db,
		service,
		time.Millisecond,
		time.Nanosecond,
		time.Hour,
		time.Second,
		time.Minute,
		1,
	)
	workerRecentMaxPages(worker, 1, 1)
	workerFullHistoryMaxPages(worker, 1, 1)

	processed, err := worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)
	require.Equal(t, 2, server.ListPullCount())
	require.Equal(t, 2, server.ListIssueCount())

	var refreshed database.RepoChangeSyncState
	require.NoError(t, db.WithContext(ctx).Where("id = ?", state.ID).First(&refreshed).Error)
	require.Equal(t, 2, refreshed.RecentPRRepairCursorPage)
	require.NotNil(t, refreshed.LastRecentPRRepairFinishedAt)
	require.NotNil(t, refreshed.LastFullHistoryRepairFinishedAt)
}

func TestChangeSyncWorkerFullHistoryRepairClosesStaleIssueWhenPullRowIsAlreadyFresh(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open("sqlite://" + filepath.Join(t.TempDir(), "change-sync.db"))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	fixture := testfixtures.CreateLocalPullRepo(t)
	server := newBackfillGitHubServer(t, fixture)
	defer server.Close()

	client := github.NewClient(server.URL, github.AuthConfig{})
	indexer := gitindex.NewService(db, client, filepath.Join(t.TempDir(), "mirrors"))
	service := githubsync.NewService(db, client, indexer)

	state, err := service.ConfigureRepoBackfill(ctx, "acme", "widgets", "full_history", 5)
	require.NoError(t, err)

	repairAt := time.Now().UTC().Add(2 * time.Hour)
	server.SetPullState(101, "closed", repairAt)
	for _, number := range []int{101, 102, 103} {
		seedServerPullState(t, ctx, service, state.RepositoryID, server, number)
	}

	staleAt := repairAt.Add(-time.Hour)
	require.NoError(t, db.WithContext(ctx).
		Model(&database.Issue{}).
		Where("repository_id = ? AND number = ?", state.RepositoryID, 101).
		Updates(map[string]any{
			"state":             "open",
			"github_updated_at": staleAt,
			"closed_at":         nil,
		}).Error)
	require.NoError(t, db.WithContext(ctx).
		Model(&database.RepoChangeSyncState{}).
		Where("id = ?", state.ID).
		Updates(map[string]any{
			"last_successful_recent_pr_repair_at": time.Now().UTC(),
			"last_recent_pr_repair_finished_at":   time.Now().UTC(),
			"last_recent_pr_repair_requested_at":  nil,
		}).Error)

	worker := githubsync.NewChangeSyncWorker(
		db,
		service,
		time.Millisecond,
		time.Nanosecond,
		time.Hour,
		time.Second,
		time.Minute,
		1,
	)
	workerFullHistoryMaxPages(worker, 1, 1)

	processed, err := worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)

	var issue database.Issue
	require.NoError(t, db.WithContext(ctx).
		Where("repository_id = ? AND number = ?", state.RepositoryID, 101).
		First(&issue).Error)
	require.Equal(t, "closed", issue.State)
	require.NotNil(t, issue.ClosedAt)
	require.Equal(t, 0, server.GetPullCount())
	require.GreaterOrEqual(t, server.GetIssueCount(), 1)
}

func TestChangeSyncWorkerFullHistoryRepairRunsBeforeTargetedRefreshBurst(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open("sqlite://" + filepath.Join(t.TempDir(), "change-sync.db"))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	fixture := testfixtures.CreateLocalPullRepo(t)
	server := newBackfillGitHubServer(t, fixture)
	defer server.Close()

	client := github.NewClient(server.URL, github.AuthConfig{})
	indexer := gitindex.NewService(db, client, filepath.Join(t.TempDir(), "mirrors"))
	service := githubsync.NewService(db, client, indexer)

	state, err := service.ConfigureRepoBackfill(ctx, "acme", "widgets", "full_history", 5)
	require.NoError(t, err)

	staleAt := time.Now().UTC().Add(-2 * time.Hour)
	for _, number := range []int{101, 102, 103} {
		seedServerPullState(t, ctx, service, state.RepositoryID, server, number)
	}
	server.SetPullState(101, "closed", time.Now().UTC().Add(2*time.Hour))
	require.NoError(t, db.WithContext(ctx).
		Model(&database.PullRequest{}).
		Where("repository_id = ? AND number = ?", state.RepositoryID, 101).
		Updates(map[string]any{
			"state":             "open",
			"github_updated_at": staleAt,
		}).Error)
	require.NoError(t, service.EnqueuePullRequestRefresh(ctx, state.RepositoryID, 101, time.Now().UTC()))

	worker := githubsync.NewChangeSyncWorker(
		db,
		service,
		time.Millisecond,
		time.Nanosecond,
		time.Hour,
		time.Second,
		time.Minute,
		1,
	)

	processed, err := worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)
	require.GreaterOrEqual(t, server.GetPullCount(), 1)

	status, err := service.GetRepoChangeStatus(ctx, "acme", "widgets")
	require.NoError(t, err)
	require.True(t, status.TargetedRefreshPending)

	var pull database.PullRequest
	require.NoError(t, db.WithContext(ctx).
		Where("repository_id = ? AND number = ?", state.RepositoryID, 101).
		First(&pull).Error)
	require.Equal(t, "closed", pull.State)
}

func TestRepairPhaseStatusHelpers(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open("sqlite://" + filepath.Join(t.TempDir(), "change-sync.db"))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	fixture := testfixtures.CreateLocalPullRepo(t)
	server := newBackfillGitHubServer(t, fixture)
	defer server.Close()

	client := github.NewClient(server.URL, github.AuthConfig{})
	indexer := gitindex.NewService(db, client, filepath.Join(t.TempDir(), "mirrors"))
	service := githubsync.NewService(db, client, indexer)

	state, err := service.ConfigureRepoBackfill(ctx, "acme", "widgets", "full_history", 5)
	require.NoError(t, err)

	now := time.Now().UTC()
	recentStart := now.Add(-5 * time.Minute)
	fullStart := now.Add(-3 * time.Minute)
	recentSuccess := now.Add(-10 * time.Minute)
	fullSuccess := now.Add(-2 * time.Minute)
	recentFail := now.Add(-8 * time.Minute)
	fullFail := now.Add(-time.Minute)

	require.NoError(t, db.WithContext(ctx).
		Model(&database.RepoChangeSyncState{}).
		Where("repository_id = ?", state.RepositoryID).
		Updates(map[string]any{
			"recent_pr_repair_lease_heartbeat_at":    now,
			"recent_pr_repair_lease_until":           now.Add(time.Minute),
			"last_recent_pr_repair_started_at":       recentStart,
			"last_full_history_repair_started_at":    fullStart,
			"last_successful_recent_pr_repair_at":    recentSuccess,
			"last_successful_full_history_repair_at": fullSuccess,
			"last_recent_pr_repair_finished_at":      recentFail,
			"last_recent_pr_repair_error":            "recent failed",
			"last_full_history_repair_finished_at":   fullFail,
			"last_full_history_repair_error":         "full failed",
		}).Error)

	status, err := service.GetRepoChangeStatus(ctx, "acme", "widgets")
	require.NoError(t, err)
	require.Equal(t, "recent_pr_repair", status.CurrentPhase)
	require.NotNil(t, status.CurrentPhaseStartedAt)
	require.Equal(t, recentStart, *status.CurrentPhaseStartedAt)
	require.Equal(t, "full_history_repair", status.LastSuccessfulRepairPhase)
	require.Equal(t, "full_history_repair", status.LastFailedRepairPhase)
	require.Equal(t, "full failed", status.LastRepairError)
	require.Equal(t, "recent failed", status.LastRecentPRRepairError)
	require.Equal(t, "full failed", status.LastFullHistoryRepairError)
}

func TestChangeSyncWorkerRepairMetricsRecorded(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open("sqlite://" + filepath.Join(t.TempDir(), "change-sync.db"))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	fixture := testfixtures.CreateLocalPullRepo(t)
	server := newBackfillGitHubServer(t, fixture)
	defer server.Close()

	client := github.NewClient(server.URL, github.AuthConfig{})
	indexer := gitindex.NewService(db, client, filepath.Join(t.TempDir(), "mirrors"))
	service := githubsync.NewService(db, client, indexer)

	state, err := service.ConfigureRepoBackfill(ctx, "acme", "widgets", "open_only", 5)
	require.NoError(t, err)

	staleAt := time.Now().UTC().Add(-2 * time.Hour)
	seedStalePull(t, ctx, db, fixture, state.RepositoryID, 101, staleAt)
	repairAt := time.Now().UTC().Add(2 * time.Hour)
	server.SetPullState(101, "closed", repairAt)

	_, err = service.RequestRecentPRRepair(ctx, "acme", "widgets")
	require.NoError(t, err)

	worker := githubsync.NewChangeSyncWorker(
		db,
		service,
		time.Millisecond,
		time.Nanosecond,
		time.Hour,
		time.Second,
		time.Minute,
		1,
	)

	processed, err := worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)

	metrics := service.GetChangeSyncMetrics(ctx)
	encoded, err := json.Marshal(metrics)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(encoded, &payload))

	changeSync := payload["repair"].(map[string]any)
	recent := changeSync["recent_pr_repair"].(map[string]any)
	totals := recent["totals"].(map[string]any)
	require.EqualValues(t, 1, totals["passes"])
	require.GreaterOrEqual(t, totals["pulls_scanned"].(float64), float64(1))
	require.GreaterOrEqual(t, totals["pulls_stale"].(float64), float64(1))
	require.GreaterOrEqual(t, totals["pulls_unchanged"].(float64), float64(0))
	require.GreaterOrEqual(t, totals["pull_fetches"].(float64), float64(1))
	require.GreaterOrEqual(t, totals["pulls_repaired"].(float64), float64(1))
	require.GreaterOrEqual(t, totals["total_lease_wait_ms"].(float64), float64(0))
	require.GreaterOrEqual(t, totals["timeouts"].(float64), float64(0))
}

type backfillGitHubServer struct {
	*httptest.Server
	mu             sync.Mutex
	listPullCount  int
	listIssueCount int
	getPullCount   int
	getIssueCount  int
	onListPull     func()
	pulls          map[int]github.PullRequestResponse
	issues         map[int]github.IssueResponse
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

func (s *backfillGitHubServer) recordListIssue() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listIssueCount++
}

func (s *backfillGitHubServer) ListIssueCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listIssueCount
}

func (s *backfillGitHubServer) recordGetPull() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getPullCount++
}

func (s *backfillGitHubServer) GetPullCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getPullCount
}

func (s *backfillGitHubServer) recordGetIssue() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getIssueCount++
}

func (s *backfillGitHubServer) GetIssueCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getIssueCount
}

func (s *backfillGitHubServer) SetPullState(number int, state string, updatedAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	pull := s.pulls[number]
	pull.State = state
	pull.UpdatedAt = updatedAt.UTC()
	s.pulls[number] = pull

	issue := s.issues[number]
	issue.State = state
	issue.UpdatedAt = pull.UpdatedAt
	if state == "closed" {
		issue.ClosedAt = &pull.UpdatedAt
	} else {
		issue.ClosedAt = nil
	}
	s.issues[number] = issue
}

func (s *backfillGitHubServer) SetIssueState(number int, state string, updatedAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	issue := s.issues[number]
	issue.State = state
	issue.UpdatedAt = updatedAt.UTC()
	if state == "closed" {
		issue.ClosedAt = &issue.UpdatedAt
	} else {
		issue.ClosedAt = nil
	}
	s.issues[number] = issue
}

func seedStalePull(t *testing.T, ctx context.Context, db *gorm.DB, fixture testfixtures.LocalPullRepo, repositoryID uint, number int, staleAt time.Time) {
	t.Helper()

	issue := database.Issue{
		ID:                uint(10000 + number),
		RepositoryID:      repositoryID,
		GitHubID:          int64(1000 + number),
		NodeID:            "I_" + strconv.Itoa(number),
		Number:            number,
		Title:             "PR " + strconv.Itoa(number),
		Body:              "stale open issue",
		State:             "open",
		IsPullRequest:     true,
		PullRequestAPIURL: "https://api.github.test/repos/acme/widgets/pulls/" + strconv.Itoa(number),
		HTMLURL:           "https://github.com/acme/widgets/pull/" + strconv.Itoa(number),
		APIURL:            "https://api.github.test/repos/acme/widgets/issues/" + strconv.Itoa(number),
		GitHubCreatedAt:   staleAt,
		GitHubUpdatedAt:   staleAt,
	}
	require.NoError(t, db.WithContext(ctx).Create(&issue).Error)

	pull := database.PullRequest{
		IssueID:         issue.ID,
		RepositoryID:    repositoryID,
		GitHubID:        int64(2000 + number),
		NodeID:          "PR_" + strconv.Itoa(number),
		Number:          number,
		State:           "open",
		HeadRef:         fixture.Pulls[number].HeadRef,
		HeadSHA:         fixture.Pulls[number].HeadSHA,
		BaseRef:         "main",
		BaseSHA:         fixture.BaseSHA,
		ChangedFiles:    2,
		CommitsCount:    1,
		HTMLURL:         "https://github.com/acme/widgets/pull/" + strconv.Itoa(number),
		APIURL:          "https://api.github.test/repos/acme/widgets/pulls/" + strconv.Itoa(number),
		GitHubCreatedAt: staleAt,
		GitHubUpdatedAt: staleAt,
	}
	require.NoError(t, db.WithContext(ctx).Create(&pull).Error)
}

func seedServerPullState(t *testing.T, ctx context.Context, service *githubsync.Service, repositoryID uint, server *backfillGitHubServer, number int) {
	t.Helper()

	server.mu.Lock()
	issue := server.issues[number]
	pull := server.pulls[number]
	server.mu.Unlock()

	_, err := service.UpsertIssue(ctx, repositoryID, issue)
	require.NoError(t, err)
	require.NoError(t, service.UpsertPullRequest(ctx, repositoryID, pull))
}

func workerRecentMaxPages(worker *githubsync.ChangeSyncWorker, maxPages, perPage int) {
	workerValue := reflect.ValueOf(worker).Elem()
	setUnexportedInt(workerValue.FieldByName("recentPRRepairMaxPages"), int64(maxPages))
	setUnexportedInt(workerValue.FieldByName("recentPRRepairPerPage"), int64(perPage))
}

func workerFullHistoryMaxPages(worker *githubsync.ChangeSyncWorker, maxPages, perPage int) {
	workerValue := reflect.ValueOf(worker).Elem()
	setUnexportedInt(workerValue.FieldByName("fullHistoryRepairMaxPages"), int64(maxPages))
	setUnexportedInt(workerValue.FieldByName("fullHistoryRepairPerPage"), int64(perPage))
}

func setUnexportedInt(field reflect.Value, value int64) {
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().SetInt(value)
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

	server := &backfillGitHubServer{
		pulls:  pulls,
		issues: issues,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/widgets", func(w http.ResponseWriter, r *http.Request) {
		writeBackfillJSON(t, w, repo)
	})
	mux.HandleFunc("/repos/acme/widgets/pulls", func(w http.ResponseWriter, r *http.Request) {
		server.recordListPull()
		if server.onListPull != nil {
			server.onListPull()
		}
		server.mu.Lock()
		defer server.mu.Unlock()

		allPulls := make([]github.PullRequestResponse, 0, len(server.pulls))
		for _, pull := range server.pulls {
			allPulls = append(allPulls, pull)
		}
		sort.Slice(allPulls, func(i, j int) bool {
			if allPulls[i].UpdatedAt.Equal(allPulls[j].UpdatedAt) {
				return allPulls[i].Number > allPulls[j].Number
			}
			return allPulls[i].UpdatedAt.After(allPulls[j].UpdatedAt)
		})

		stateFilter := strings.TrimSpace(r.URL.Query().Get("state"))
		if stateFilter != "" && stateFilter != "all" {
			filtered := allPulls[:0]
			for _, pull := range allPulls {
				if pull.State == stateFilter {
					filtered = append(filtered, pull)
				}
			}
			allPulls = filtered
		}

		page := 1
		if raw := strings.TrimSpace(r.URL.Query().Get("page")); raw != "" {
			parsed, err := strconv.Atoi(raw)
			require.NoError(t, err)
			if parsed > 0 {
				page = parsed
			}
		}
		perPage := 100
		if raw := strings.TrimSpace(r.URL.Query().Get("per_page")); raw != "" {
			parsed, err := strconv.Atoi(raw)
			require.NoError(t, err)
			if parsed > 0 {
				perPage = parsed
			}
		}
		start := (page - 1) * perPage
		if start >= len(allPulls) {
			writeBackfillJSON(t, w, []github.PullRequestResponse{})
			return
		}
		end := start + perPage
		if end > len(allPulls) {
			end = len(allPulls)
		}
		writeBackfillJSON(t, w, allPulls[start:end])
	})
	mux.HandleFunc("/repos/acme/widgets/issues", func(w http.ResponseWriter, r *http.Request) {
		server.recordListIssue()
		server.mu.Lock()
		defer server.mu.Unlock()

		allIssues := make([]github.IssueResponse, 0, len(server.issues))
		for _, issue := range server.issues {
			allIssues = append(allIssues, issue)
		}
		sort.Slice(allIssues, func(i, j int) bool {
			if allIssues[i].UpdatedAt.Equal(allIssues[j].UpdatedAt) {
				return allIssues[i].Number > allIssues[j].Number
			}
			return allIssues[i].UpdatedAt.After(allIssues[j].UpdatedAt)
		})

		stateFilter := strings.TrimSpace(r.URL.Query().Get("state"))
		if stateFilter != "" && stateFilter != "all" {
			filtered := allIssues[:0]
			for _, issue := range allIssues {
				if issue.State == stateFilter {
					filtered = append(filtered, issue)
				}
			}
			allIssues = filtered
		}

		page := 1
		if raw := strings.TrimSpace(r.URL.Query().Get("page")); raw != "" {
			parsed, err := strconv.Atoi(raw)
			require.NoError(t, err)
			if parsed > 0 {
				page = parsed
			}
		}
		perPage := 100
		if raw := strings.TrimSpace(r.URL.Query().Get("per_page")); raw != "" {
			parsed, err := strconv.Atoi(raw)
			require.NoError(t, err)
			if parsed > 0 {
				perPage = parsed
			}
		}
		start := (page - 1) * perPage
		if start >= len(allIssues) {
			writeBackfillJSON(t, w, []github.IssueResponse{})
			return
		}
		end := start + perPage
		if end > len(allIssues) {
			end = len(allIssues)
		}
		writeBackfillJSON(t, w, allIssues[start:end])
	})
	mux.HandleFunc("/repos/acme/widgets/issues/", func(w http.ResponseWriter, r *http.Request) {
		number, ok := tailNumber(r.URL.Path, "/repos/acme/widgets/issues/")
		require.True(t, ok)
		server.recordGetIssue()
		server.mu.Lock()
		defer server.mu.Unlock()
		writeBackfillJSON(t, w, server.issues[number])
	})
	mux.HandleFunc("/repos/acme/widgets/pulls/", func(w http.ResponseWriter, r *http.Request) {
		number, ok := tailNumber(r.URL.Path, "/repos/acme/widgets/pulls/")
		require.True(t, ok)
		server.recordGetPull()
		server.mu.Lock()
		defer server.mu.Unlock()
		writeBackfillJSON(t, w, server.pulls[number])
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
