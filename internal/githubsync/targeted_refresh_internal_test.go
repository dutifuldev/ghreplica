package githubsync

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/dutifuldev/ghreplica/internal/github"
	"github.com/stretchr/testify/require"
)

func TestAcquireNextTargetedRefreshPrefersNeverAttemptedNewest(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open("sqlite://" + filepath.Join(t.TempDir(), "targeted-refresh.db"))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	repo := database.Repository{
		GitHubID:   101,
		OwnerLogin: "acme",
		Name:       "widgets",
		FullName:   "acme/widgets",
	}
	require.NoError(t, db.Create(&repo).Error)

	now := time.Now().UTC()
	require.NoError(t, db.Create(&database.RepoTargetedPullRefresh{
		RepositoryID:      repo.ID,
		PullRequestNumber: 1,
		RequestedAt:       timePtr(now.Add(-3 * time.Hour)),
		LastAttemptedAt:   timePtr(now.Add(-10 * time.Minute)),
		AttemptCount:      2,
		NextAttemptAt:     timePtr(now.Add(-time.Minute)),
	}).Error)
	require.NoError(t, db.Create(&database.RepoTargetedPullRefresh{
		RepositoryID:      repo.ID,
		PullRequestNumber: 2,
		RequestedAt:       timePtr(now.Add(-2 * time.Hour)),
		AttemptCount:      0,
	}).Error)
	require.NoError(t, db.Create(&database.RepoTargetedPullRefresh{
		RepositoryID:      repo.ID,
		PullRequestNumber: 3,
		RequestedAt:       timePtr(now.Add(-time.Hour)),
		AttemptCount:      0,
	}).Error)

	worker := NewChangeSyncWorker(
		db,
		NewService(db, github.NewClient("https://api.github.test", github.AuthConfig{})),
		time.Second,
		time.Second,
		time.Hour,
		time.Minute,
		time.Minute,
		10,
	)

	row, ok, err := worker.acquireNextTargetedRefresh(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 3, row.PullRequestNumber)
	require.Equal(t, 0, row.AttemptCount)
}

func TestFinishTargetedRefreshParksRowAfterFifthFailure(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open("sqlite://" + filepath.Join(t.TempDir(), "targeted-refresh-failure.db"))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	repo := database.Repository{
		GitHubID:   101,
		OwnerLogin: "acme",
		Name:       "widgets",
		FullName:   "acme/widgets",
	}
	require.NoError(t, db.Create(&repo).Error)
	require.NoError(t, db.Create(&database.RepoChangeSyncState{
		RepositoryID:           repo.ID,
		TargetedRefreshPending: true,
	}).Error)

	now := time.Now().UTC()
	row := database.RepoTargetedPullRefresh{
		RepositoryID:      repo.ID,
		PullRequestNumber: 99,
		RequestedAt:       timePtr(now.Add(-time.Hour)),
		AttemptCount:      4,
		LeaseOwnerID:      "test-owner",
		LeaseStartedAt:    timePtr(now.Add(-time.Minute)),
		LeaseHeartbeatAt:  timePtr(now.Add(-time.Second)),
		LeaseUntil:        timePtr(now.Add(time.Minute)),
	}
	require.NoError(t, db.Create(&row).Error)

	worker := NewChangeSyncWorker(
		db,
		NewService(db, github.NewClient("https://api.github.test", github.AuthConfig{})),
		time.Second,
		time.Second,
		time.Hour,
		time.Minute,
		time.Minute,
		10,
	)
	worker.leases.ownerID = "test-owner"

	err = worker.finishTargetedRefresh(ctx, row, fmt.Errorf("context deadline exceeded"))
	require.EqualError(t, err, "context deadline exceeded")

	var stored database.RepoTargetedPullRefresh
	require.NoError(t, db.First(&stored, row.ID).Error)
	require.Equal(t, 5, stored.AttemptCount)
	require.NotNil(t, stored.ParkedAt)
	require.Nil(t, stored.NextAttemptAt)
	require.Empty(t, stored.LeaseOwnerID)

	var state database.RepoChangeSyncState
	require.NoError(t, db.Where("repository_id = ?", repo.ID).First(&state).Error)
	require.False(t, state.TargetedRefreshPending)
	require.Equal(t, "context deadline exceeded", state.LastError)
}

func timePtr(value time.Time) *time.Time {
	utc := value.UTC()
	return &utc
}
