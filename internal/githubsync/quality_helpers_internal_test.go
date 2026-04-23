package githubsync

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	gh "github.com/dutifuldev/ghreplica/internal/github"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestServiceHelpersAndDeletePaths(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openGitHubSyncTestDB(t)
	service := NewService(db, nil)

	withoutSearch := service.WithoutSearch()
	require.Nil(t, withoutSearch.search)
	require.NotNil(t, service.search)

	updatedAge := service.WithOpenPRInventoryMaxAge(30 * time.Minute)
	require.Equal(t, 30*time.Minute, updatedAge.openPRInventoryMaxAge)
	require.Equal(t, 6*time.Hour, service.openPRInventoryMaxAge)

	repo := database.Repository{ID: 1, FullName: "acme/widgets", OwnerLogin: "acme", Name: "widgets"}
	issue := database.Issue{ID: 11, RepositoryID: repo.ID, GitHubID: 101, Number: 7, Title: "Issue", State: "open"}
	comment := database.IssueComment{ID: 21, RepositoryID: repo.ID, GitHubID: 201, IssueID: issue.ID, Body: "comment"}
	reviewComment := database.PullRequestReviewComment{ID: 31, RepositoryID: repo.ID, GitHubID: 301, PullRequestID: issue.ID, Body: "review"}
	require.NoError(t, db.Create(&repo).Error)
	require.NoError(t, db.Create(&issue).Error)
	require.NoError(t, db.Create(&comment).Error)
	require.NoError(t, db.Create(&reviewComment).Error)

	require.NoError(t, withoutSearch.DeleteIssue(ctx, repo.ID, issueResponseForNumber(issue.Number, issue.GitHubID)))
	require.NoError(t, withoutSearch.DeleteIssueComment(ctx, repo.ID, issueCommentResponseForID(comment.GitHubID)))
	require.NoError(t, withoutSearch.DeletePullRequestReviewComment(ctx, repo.ID, reviewCommentResponseForID(reviewComment.GitHubID)))

	var count int64
	require.NoError(t, db.Model(&database.Issue{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Model(&database.IssueComment{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Model(&database.PullRequestReviewComment{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestDeletePathsWithSearchAndWorkerDefaults(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openGitHubSyncTestDB(t)
	service := NewService(db, nil)

	require.NoError(t, db.Create(&database.SearchDocument{
		RepositoryID:     1,
		DocumentType:     "issue",
		DocumentGitHubID: 101,
		Number:           7,
		SearchText:       "issue",
		ObjectUpdatedAt:  time.Now().UTC(),
	}).Error)
	require.NoError(t, db.Create(&database.SearchDocument{
		RepositoryID:     1,
		DocumentType:     "issue_comment",
		DocumentGitHubID: 201,
		Number:           7,
		SearchText:       "comment",
		ObjectUpdatedAt:  time.Now().UTC(),
	}).Error)
	require.NoError(t, db.Create(&database.SearchDocument{
		RepositoryID:     1,
		DocumentType:     "pull_request_review_comment",
		DocumentGitHubID: 301,
		Number:           7,
		SearchText:       "review",
		ObjectUpdatedAt:  time.Now().UTC(),
	}).Error)

	require.NoError(t, service.deleteIssue(ctx, 1, issueResponseForNumber(7, 101)))
	require.NoError(t, service.deleteIssueComment(ctx, 1, issueCommentResponseForID(201)))
	require.NoError(t, service.deletePullRequestReviewComment(ctx, 1, reviewCommentResponseForID(301)))

	var docs int64
	require.NoError(t, db.Model(&database.SearchDocument{}).Count(&docs).Error)
	require.Zero(t, docs)

	service.repairMetrics = nil
	require.Equal(t, map[string]any{}, service.GetChangeSyncMetrics(ctx))

	worker := NewChangeSyncWorker(db, service, 0, 0, 0, 0, 0, 0)
	require.Equal(t, 5*time.Second, worker.pollInterval)
	require.Equal(t, 15*time.Second, worker.webhookRefreshDebounce)
	require.Equal(t, 6*time.Hour, worker.openPRInventoryMaxAge)
	require.Equal(t, 15*time.Minute, worker.leaseTTL)
	require.Equal(t, 30*time.Minute, worker.backfillMaxRuntime)
	require.Equal(t, 1000, worker.backfillMaxPRsPerPass)
}

func TestNoteRepositoryWebhookAndMarkRepositoryChangeDirty(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openGitHubSyncTestDB(t)
	service := NewService(db, nil)

	seenAt := time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC)
	require.NoError(t, service.NoteRepositoryWebhook(ctx, 55, seenAt))

	var state database.RepoChangeSyncState
	require.NoError(t, db.Where("repository_id = ?", 55).First(&state).Error)
	require.NotNil(t, state.LastWebhookAt)
	require.Equal(t, seenAt, state.LastWebhookAt.UTC())

	later := seenAt.Add(5 * time.Minute)
	require.NoError(t, service.MarkRepositoryChangeDirty(ctx, 55, later))
	require.NoError(t, db.Where("repository_id = ?", 55).First(&state).Error)
	require.True(t, state.Dirty)
	require.NotNil(t, state.DirtySince)
	require.Equal(t, later, state.DirtySince.UTC())
	require.NotNil(t, state.LastRequestedFetchAt)
	require.Equal(t, later, state.LastRequestedFetchAt.UTC())
}

func TestTargetedRefreshAndMetricsHelpers(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openGitHubSyncTestDB(t)
	service := NewService(db, nil)

	require.NoError(t, db.Create(&database.RepoOpenPullInventory{
		RepositoryID:      9,
		Generation:        4,
		PullRequestNumber: 101,
		FreshnessState:    "stale_head_changed",
		GitHubUpdatedAt:   time.Now().UTC(),
	}).Error)

	ok, err := service.hasAnyBackfillCandidates(ctx, 9, 4)
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = service.hasAnyBackfillCandidates(ctx, 9, 5)
	require.NoError(t, err)
	require.False(t, ok)

	require.Equal(t, time.Minute, targetedRefreshBackoff(1))
	require.Equal(t, 5*time.Minute, targetedRefreshBackoff(2))
	require.Equal(t, 15*time.Minute, targetedRefreshBackoff(3))
	require.Equal(t, time.Hour, targetedRefreshBackoff(4))

	metrics := newRepairMetricsRegistry()
	metrics.recordFailure(recentRepairPhase, 9, "acme/widgets", time.Second, 2*time.Second, context.DeadlineExceeded)
	snapshot := metrics.snapshot(ctx)
	recent := snapshot[string(recentRepairPhase)].(map[string]any)
	totals := recent["totals"].(repairRepoMetrics)
	require.EqualValues(t, 1, totals.Failures)
	require.EqualValues(t, 1, totals.Timeouts)
}

func TestChangeSyncSmallHelpers(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openGitHubSyncTestDB(t)
	service := NewService(db, nil)
	now := time.Now().UTC()

	require.Equal(t, changeBackfillModeOff, normalizeBackfillMode(""))
	require.Equal(t, changeBackfillModeFullHistory, normalizeBackfillMode(changeBackfillModeFullHistory))
	require.Equal(t, changeBackfillModeOpenOnly, normalizeBackfillMode("unknown"))

	phase, startedAt := currentRepoPhase(now, repoChangeStatusRow{
		RecentPRRepairLeaseHeartbeatAt: helperTimePtr(now),
		RecentPRRepairLeaseUntil:       helperTimePtr(now.Add(2 * time.Second)),
		LastRecentPRRepairStartedAt:    helperTimePtr(now.Add(-time.Minute)),
	})
	require.Equal(t, string(recentRepairPhase), phase)
	require.NotNil(t, startedAt)

	require.Equal(t, "current", desiredFreshness(&database.PullRequestChangeSnapshot{
		HeadSHA:        "head",
		BaseSHA:        "base",
		BaseRef:        "main",
		IndexFreshness: "current",
	}, gh.PullRequestResponse{
		Head: gh.PullBranch{SHA: "head"},
		Base: gh.PullBranch{SHA: "base", Ref: "refs/heads/main"},
	}))
	require.Equal(t, "stale_head_changed", desiredFreshness(&database.PullRequestChangeSnapshot{
		HeadSHA: "old",
		BaseSHA: "base",
		BaseRef: "main",
	}, gh.PullRequestResponse{
		Head: gh.PullBranch{SHA: "head"},
		Base: gh.PullBranch{SHA: "base", Ref: "main"},
	}))
	require.Equal(t, 4, maxInt(4, 2))

	require.NoError(t, db.Create(&database.RepoChangeSyncState{ID: 8, RepositoryID: 88}).Error)
	state, err := service.repoChangeStateOptional(ctx, 88)
	require.NoError(t, err)
	require.NotNil(t, state)

	snapshot := database.PullRequestChangeSnapshot{
		ID:                9,
		RepositoryID:      88,
		PullRequestID:     1,
		PullRequestNumber: 101,
		HeadSHA:           "head",
		BaseSHA:           "base",
		MergeBaseSHA:      "merge",
		BaseRef:           "main",
		State:             "open",
		IndexedAs:         "full",
		IndexFreshness:    "current",
	}
	require.NoError(t, db.Create(&snapshot).Error)

	foundSnapshot, err := service.pullRequestSnapshotOptional(ctx, 88, 101)
	require.NoError(t, err)
	require.NotNil(t, foundSnapshot)

	require.NoError(t, service.applySnapshotFreshnessUpdates(ctx, map[string][]uint{"stale_base_moved": []uint{snapshot.ID}}, now))
	require.NoError(t, db.First(&snapshot, snapshot.ID).Error)
	require.Equal(t, "stale_base_moved", snapshot.IndexFreshness)

	current, stale := adjustBackfillCounts(2, 3, "current", "stale_head_changed")
	require.Equal(t, 1, current)
	require.Equal(t, 4, stale)

	phase, _ = currentRepoPhase(now, repoChangeStatusRow{
		FullHistoryRepairLeaseHeartbeatAt: helperTimePtr(now),
		FullHistoryRepairLeaseUntil:       helperTimePtr(now.Add(2 * time.Second)),
	})
	require.Equal(t, string(fullHistoryRepairPhase), phase)

	phase, _ = currentRepoPhase(now, repoChangeStatusRow{
		FetchLeaseHeartbeatAt: helperTimePtr(now),
		FetchLeaseUntil:       helperTimePtr(now.Add(2 * time.Second)),
	})
	require.Equal(t, "inventory_scan", phase)

	phase, _ = currentRepoPhase(now, repoChangeStatusRow{
		BackfillLeaseHeartbeatAt: helperTimePtr(now),
		BackfillLeaseUntil:       helperTimePtr(now.Add(2 * time.Second)),
	})
	require.Equal(t, "backfill", phase)

	phase, _ = currentRepoPhase(now, repoChangeStatusRow{
		TargetedRefreshLeaseHeartbeatAt: helperTimePtr(now),
		TargetedRefreshLeaseUntil:       helperTimePtr(now.Add(2 * time.Second)),
	})
	require.Equal(t, "targeted_refresh", phase)

	phase, _ = currentRepoPhase(now, repoChangeStatusRow{})
	require.Equal(t, "", phase)

	missingState, err := service.repoChangeStateOptional(ctx, 999)
	require.NoError(t, err)
	require.Nil(t, missingState)

	missingSnapshot, err := service.pullRequestSnapshotOptional(ctx, 88, 999)
	require.NoError(t, err)
	require.Nil(t, missingSnapshot)
}

func TestSanitizeAndExistingSyncModeHelpers(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openGitHubSyncTestDB(t)
	service := NewService(db, nil)

	require.Equal(t, "hello", sanitizeProjectedText("he\x00llo"))
	require.JSONEq(t, `{"key":"value","items":["x","y"]}`, string(sanitizeRawJSON([]byte(`{"ke\u0000y":"va\u0000lue","items":["x","\u0000y"]}`))))
	require.Equal(t, `{"bad":"\u0000"`, string(sanitizeRawJSON([]byte(`{"bad":"\u0000"`))))

	mode, err := service.existingSyncMode(ctx, "acme/widgets", nil)
	require.NoError(t, err)
	require.Equal(t, "manual_backfill", mode)

	repoID := uint(7)
	require.NoError(t, db.Create(&database.TrackedRepository{
		Owner:        "acme",
		Name:         "widgets",
		FullName:     "acme/widgets",
		RepositoryID: &repoID,
		SyncMode:     "",
	}).Error)
	mode, err = service.existingSyncMode(ctx, "acme/widgets", &repoID)
	require.NoError(t, err)
	require.Equal(t, "manual_backfill", mode)

	require.NoError(t, db.Model(&database.TrackedRepository{}).Where("repository_id = ?", repoID).Update("sync_mode", "webhook").Error)
	mode, err = service.existingSyncMode(ctx, "acme/widgets", &repoID)
	require.NoError(t, err)
	require.Equal(t, "webhook_only", mode)

	require.NoError(t, db.Model(&database.TrackedRepository{}).Where("repository_id = ?", repoID).Update("sync_mode", "webhook_only").Error)
	mode, err = service.existingSyncMode(ctx, "acme/widgets", &repoID)
	require.NoError(t, err)
	require.Equal(t, "webhook_only", mode)

	require.NoError(t, db.Model(&database.TrackedRepository{}).Where("repository_id = ?", repoID).Update("sync_mode", "poll").Error)
	mode, err = service.existingSyncMode(ctx, "acme/widgets", &repoID)
	require.NoError(t, err)
	require.Equal(t, "manual_backfill", mode)

	number, err := issueNumberFromURL("https://api.github.com/repos/acme/widgets/issues/17")
	require.NoError(t, err)
	require.Equal(t, 17, number)
	_, err = issueNumberFromURL("https://api.github.com/repos/acme/widgets/issues/not-a-number")
	require.Error(t, err)

	owner, name, err := splitFullName("acme/widgets")
	require.NoError(t, err)
	require.Equal(t, "acme", owner)
	require.Equal(t, "widgets", name)
	_, _, err = splitFullName("broken")
	require.Error(t, err)

	require.Equal(t, "", pullRequestURL(nil))
	require.Equal(t, "https://api.github.com/repos/acme/widgets/pulls/17", pullRequestURL(&gh.IssuePullRequestRef{
		URL: "https://api.github.com/repos/acme/widgets/pulls/17",
	}))

	repo := database.Repository{ID: 19, FullName: "acme/extra", OwnerLogin: "acme", Name: "extra"}
	issue := database.Issue{ID: 29, RepositoryID: repo.ID, GitHubID: 2901, Number: 29, Title: "PR issue", State: "open", IsPullRequest: true}
	pull := database.PullRequest{RepositoryID: repo.ID, IssueID: issue.ID, GitHubID: 3901, Number: 29, State: "open"}
	require.NoError(t, db.Create(&repo).Error)
	require.NoError(t, db.Create(&issue).Error)
	require.NoError(t, db.Create(&pull).Error)

	pullIssueID, err := service.pullRequestIssueID(ctx, repo.ID, pull.Number)
	require.NoError(t, err)
	require.Equal(t, issue.ID, pullIssueID)
	_, err = service.pullRequestIssueID(ctx, repo.ID, 999)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	now := time.Now().UTC()
	require.Equal(t, "inventory_scan", inventoryRefreshBlockingPhaseFromState(now, database.RepoChangeSyncState{
		FetchLeaseHeartbeatAt: helperTimePtr(now),
		FetchLeaseUntil:       helperTimePtr(now.Add(time.Minute)),
	}))
	require.Equal(t, "backfill", inventoryRefreshBlockingPhaseFromState(now, database.RepoChangeSyncState{
		BackfillLeaseHeartbeatAt: helperTimePtr(now),
		BackfillLeaseUntil:       helperTimePtr(now.Add(time.Minute)),
	}))
	require.Equal(t, "", inventoryRefreshBlockingPhaseFromState(now, database.RepoChangeSyncState{}))
}

func TestLeaseManagerAndFinishHelpers(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openGitHubSyncTestDB(t)
	manager := newRepoLeaseManager(db, 2*time.Second)
	state := database.RepoChangeSyncState{ID: 1, RepositoryID: 44}
	require.NoError(t, db.Create(&state).Error)

	acquired, _, err := manager.acquire(ctx, state.ID, fetchLeaseKind, time.Now().UTC())
	require.NoError(t, err)
	require.True(t, acquired)
	require.NoError(t, manager.heartbeat(ctx, state.ID, fetchLeaseKind))

	worker := &ChangeSyncWorker{db: db, leases: manager}

	runErr := errors.New("fetch failed")
	require.ErrorIs(t, worker.finishFetchStateWithError(ctx, state, runErr), runErr)
	require.NoError(t, db.First(&state, state.ID).Error)
	require.Equal(t, "fetch failed", state.LastError)
	require.Nil(t, state.FetchLeaseUntil)

	state2 := database.RepoChangeSyncState{ID: 2, RepositoryID: 45, BackfillLeaseOwnerID: manager.owner(), BackfillLeaseStartedAt: helperTimePtr(time.Now().UTC()), BackfillLeaseHeartbeatAt: helperTimePtr(time.Now().UTC()), BackfillLeaseUntil: helperTimePtr(time.Now().UTC().Add(time.Second))}
	state3 := database.RepoChangeSyncState{ID: 3, RepositoryID: 46, RecentPRRepairLeaseOwnerID: manager.owner(), RecentPRRepairLeaseStartedAt: helperTimePtr(time.Now().UTC()), RecentPRRepairLeaseHeartbeatAt: helperTimePtr(time.Now().UTC()), RecentPRRepairLeaseUntil: helperTimePtr(time.Now().UTC().Add(time.Second))}
	state4 := database.RepoChangeSyncState{ID: 4, RepositoryID: 47, FullHistoryRepairLeaseOwnerID: manager.owner(), FullHistoryRepairLeaseStartedAt: helperTimePtr(time.Now().UTC()), FullHistoryRepairLeaseHeartbeatAt: helperTimePtr(time.Now().UTC()), FullHistoryRepairLeaseUntil: helperTimePtr(time.Now().UTC().Add(time.Second))}
	require.NoError(t, db.Create(&state2).Error)
	require.NoError(t, db.Create(&state3).Error)
	require.NoError(t, db.Create(&state4).Error)

	backfillErr := errors.New("backfill failed")
	recentErr := errors.New("recent failed")
	fullErr := errors.New("full failed")
	require.ErrorIs(t, worker.finishBackfillStateWithError(ctx, state2, backfillErr), backfillErr)
	require.ErrorIs(t, worker.finishRecentPRRepairWithError(ctx, state3, recentErr), recentErr)
	require.ErrorIs(t, worker.finishFullHistoryRepairWithError(ctx, state4, fullErr), fullErr)
}

func openGitHubSyncTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := database.Open("sqlite://" + filepath.Join(t.TempDir(), "githubsync-test.db"))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))
	return db
}

func issueResponseForNumber(number int, id int64) gh.IssueResponse {
	return gh.IssueResponse{ID: id, Number: number}
}

func issueCommentResponseForID(id int64) gh.IssueCommentResponse {
	return gh.IssueCommentResponse{ID: id}
}

func reviewCommentResponseForID(id int64) gh.PullRequestReviewCommentResponse {
	return gh.PullRequestReviewCommentResponse{ID: id}
}

func helperTimePtr(value time.Time) *time.Time {
	return &value
}
