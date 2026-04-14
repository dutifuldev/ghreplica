package refresh_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/dutifuldev/ghreplica/internal/github"
	"github.com/dutifuldev/ghreplica/internal/refresh"
	"github.com/stretchr/testify/require"
)

type bootstrapperFunc func(ctx context.Context, owner, repo string) error

func (f bootstrapperFunc) BootstrapRepository(ctx context.Context, owner, repo string) error {
	return f(ctx, owner, repo)
}

func TestWorkerRetriesTemporaryErrorsThenSucceeds(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	now := time.Now().UTC()
	require.NoError(t, db.WithContext(ctx).Create(&database.RepositoryRefreshJob{
		JobType:       refresh.JobTypeBootstrapRepository,
		Owner:         "acme",
		Name:          "widgets",
		FullName:      "acme/widgets",
		Source:        "manual",
		Status:        "pending",
		MaxAttempts:   3,
		RequestedAt:   now,
		NextAttemptAt: &now,
	}).Error)

	attempts := 0
	worker := refresh.NewWorker(db, bootstrapperFunc(func(ctx context.Context, owner, repo string) error {
		attempts++
		if attempts == 1 {
			return &github.HTTPError{StatusCode: 502, Message: "temporary"}
		}
		return nil
	}), time.Millisecond)

	processed, err := worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)

	var job database.RepositoryRefreshJob
	require.NoError(t, db.WithContext(ctx).First(&job).Error)
	require.Equal(t, "pending", job.Status)
	require.Equal(t, 1, job.Attempts)
	require.NotNil(t, job.NextAttemptAt)

	past := time.Now().UTC().Add(-time.Second)
	require.NoError(t, db.WithContext(ctx).Model(&job).Updates(map[string]any{
		"next_attempt_at": past,
		"status":          "pending",
	}).Error)

	processed, err = worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)

	require.NoError(t, db.WithContext(ctx).First(&job).Error)
	require.Equal(t, "succeeded", job.Status)
	require.Equal(t, 2, job.Attempts)
}

func TestWorkerSupersedesLegacyWebhookJobsAndRecoversExpiredLeases(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	now := time.Now().UTC()
	expired := now.Add(-10 * time.Minute)

	require.NoError(t, db.WithContext(ctx).Create(&database.RepositoryRefreshJob{
		Owner:         "acme",
		Name:          "widgets",
		FullName:      "acme/widgets",
		JobType:       refresh.JobTypeBootstrapRepository,
		Source:        "webhook",
		Status:        "failed",
		MaxAttempts:   3,
		RequestedAt:   now.Add(-time.Hour),
		NextAttemptAt: &now,
	}).Error)

	require.NoError(t, db.WithContext(ctx).Create(&database.RepositoryRefreshJob{
		Owner:          "acme",
		Name:           "widgets",
		FullName:       "acme/widgets",
		JobType:        refresh.JobTypeBootstrapRepository,
		Source:         "manual",
		Status:         "processing",
		Attempts:       1,
		MaxAttempts:    3,
		RequestedAt:    now.Add(-time.Hour),
		StartedAt:      &expired,
		LeaseExpiresAt: &expired,
	}).Error)

	worker := refresh.NewWorker(db, bootstrapperFunc(func(ctx context.Context, owner, repo string) error {
		return nil
	}), time.Millisecond)

	processed, err := worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)

	var jobs []database.RepositoryRefreshJob
	require.NoError(t, db.WithContext(ctx).Order("id ASC").Find(&jobs).Error)
	require.Len(t, jobs, 2)
	require.Equal(t, "superseded", jobs[0].Status)
	require.Equal(t, "succeeded", jobs[1].Status)
	require.Equal(t, 2, jobs[1].Attempts)
	require.Nil(t, jobs[1].LeaseExpiresAt)
}

func testDatabaseURL(t *testing.T) string {
	t.Helper()
	return "sqlite://file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
}
