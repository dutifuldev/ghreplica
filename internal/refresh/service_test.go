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

func TestWorkerSupersedesWebhookRefreshJobsAndRecoversExpiredLeases(t *testing.T) {
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

func TestResolveTrackedRepositoryPrefersRepositoryIDAcrossRename(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	repo := &database.Repository{
		GitHubID:   101,
		OwnerLogin: "acme",
		Name:       "widgets-renamed",
		FullName:   "acme/widgets-renamed",
	}
	require.NoError(t, db.WithContext(ctx).Create(repo).Error)

	stable := &database.TrackedRepository{
		Owner:        "acme",
		Name:         "widgets",
		FullName:     "acme/widgets",
		RepositoryID: &repo.ID,
		SyncMode:     "webhook_only",
	}
	require.NoError(t, db.WithContext(ctx).Create(stable).Error)

	duplicate := &database.TrackedRepository{
		Owner:    "acme",
		Name:     "widgets-renamed",
		FullName: "acme/widgets-renamed",
		SyncMode: "webhook_only",
	}
	require.NoError(t, db.WithContext(ctx).Create(duplicate).Error)

	require.NoError(t, db.WithContext(ctx).Create(&database.RepositoryRefreshJob{
		TrackedRepositoryID: &duplicate.ID,
		RepositoryID:        &repo.ID,
		JobType:             refresh.JobTypeBootstrapRepository,
		FullName:            duplicate.FullName,
		Status:              "pending",
		MaxAttempts:         3,
		RequestedAt:         time.Now().UTC(),
	}).Error)

	resolved, err := refresh.ResolveTrackedRepository(ctx, db, &repo.ID, "acme/widgets-renamed")
	require.NoError(t, err)
	require.NotNil(t, resolved)
	require.Equal(t, stable.ID, resolved.ID)

	var trackedRows []database.TrackedRepository
	require.NoError(t, db.WithContext(ctx).Order("id ASC").Find(&trackedRows).Error)
	require.Len(t, trackedRows, 1)

	var job database.RepositoryRefreshJob
	require.NoError(t, db.WithContext(ctx).First(&job).Error)
	require.NotNil(t, job.TrackedRepositoryID)
	require.Equal(t, stable.ID, *job.TrackedRepositoryID)
}

func TestEnqueueRepositoryRefreshDeduplicatesJobsAcrossRepositoryIDBackfill(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	tracked := &database.TrackedRepository{
		Owner:    "acme",
		Name:     "widgets",
		FullName: "acme/widgets",
		SyncMode: "webhook_only",
		Enabled:  true,
	}
	require.NoError(t, db.WithContext(ctx).Create(tracked).Error)

	scheduler := refresh.NewScheduler(db)
	request := refresh.Request{
		Owner:    "acme",
		Name:     "widgets",
		FullName: "acme/widgets",
	}
	require.NoError(t, scheduler.EnqueueRepositoryRefresh(ctx, request))

	repo := &database.Repository{
		GitHubID:   101,
		OwnerLogin: "acme",
		Name:       "widgets",
		FullName:   "acme/widgets",
	}
	require.NoError(t, db.WithContext(ctx).Create(repo).Error)
	require.NoError(t, db.WithContext(ctx).Model(&database.TrackedRepository{}).
		Where("id = ?", tracked.ID).
		Update("repository_id", repo.ID).Error)

	require.NoError(t, scheduler.EnqueueRepositoryRefresh(ctx, request))

	var jobs []database.RepositoryRefreshJob
	require.NoError(t, db.WithContext(ctx).Order("id ASC").Find(&jobs).Error)
	require.Len(t, jobs, 1)
	require.NotNil(t, jobs[0].TrackedRepositoryID)
	require.Equal(t, tracked.ID, *jobs[0].TrackedRepositoryID)
}

func TestWorkerUsesCurrentRepositoryLocatorForRenamedJob(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open(testDatabaseURL(t))
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	repo := &database.Repository{
		GitHubID:   101,
		OwnerLogin: "acme",
		Name:       "widgets-renamed",
		FullName:   "acme/widgets-renamed",
	}
	require.NoError(t, db.WithContext(ctx).Create(repo).Error)

	tracked := &database.TrackedRepository{
		Owner:        "acme",
		Name:         "widgets-renamed",
		FullName:     "acme/widgets-renamed",
		RepositoryID: &repo.ID,
		SyncMode:     "webhook_only",
		Enabled:      true,
	}
	require.NoError(t, db.WithContext(ctx).Create(tracked).Error)

	now := time.Now().UTC()
	require.NoError(t, db.WithContext(ctx).Create(&database.RepositoryRefreshJob{
		TrackedRepositoryID: &tracked.ID,
		RepositoryID:        &repo.ID,
		JobType:             refresh.JobTypeBootstrapRepository,
		Owner:               "acme",
		Name:                "widgets",
		FullName:            "acme/widgets",
		Source:              "manual",
		Status:              "pending",
		MaxAttempts:         3,
		RequestedAt:         now,
		NextAttemptAt:       &now,
	}).Error)

	var calledOwner string
	var calledRepo string
	worker := refresh.NewWorker(db, bootstrapperFunc(func(ctx context.Context, owner, repo string) error {
		calledOwner = owner
		calledRepo = repo
		return nil
	}), time.Millisecond)

	processed, err := worker.RunOnce(ctx)
	require.NoError(t, err)
	require.True(t, processed)
	require.Equal(t, "acme", calledOwner)
	require.Equal(t, "widgets-renamed", calledRepo)

	var job database.RepositoryRefreshJob
	require.NoError(t, db.WithContext(ctx).First(&job).Error)
	require.Equal(t, "succeeded", job.Status)
}

func testDatabaseURL(t *testing.T) string {
	t.Helper()
	return "sqlite://file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
}
