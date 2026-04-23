package refresh

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/stretchr/testify/require"
)

type testBootstrapper func(context.Context, string, string) error

func (f testBootstrapper) BootstrapRepository(ctx context.Context, owner, repo string) error {
	return f(ctx, owner, repo)
}

func TestWebhookTrackingAndRefreshHelpers(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open("sqlite://" + filepath.Join(t.TempDir(), "refresh-coverage.db"))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	repo := database.Repository{
		ID:         1,
		GitHubID:   101,
		OwnerLogin: "acme",
		Name:       "widgets",
		FullName:   "acme/widgets",
	}
	require.NoError(t, db.Create(&repo).Error)

	seenAt := time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC)
	tracked, err := UpsertTrackedRepositoryForWebhook(ctx, db, "acme", "widgets", "acme/widgets", &repo.ID, seenAt)
	require.NoError(t, err)
	require.Equal(t, syncModeWebhookOnly, tracked.SyncMode)
	require.Equal(t, completenessEmpty, tracked.IssuesCompleteness)

	later := seenAt.Add(time.Minute)
	tracked, err = UpsertTrackedRepositoryForWebhook(ctx, db, "acme", "widgets-renamed", "acme/widgets-renamed", &repo.ID, later)
	require.NoError(t, err)
	require.Equal(t, "widgets-renamed", tracked.Name)
	require.NotNil(t, tracked.LastWebhookAt)
	require.Equal(t, later, tracked.LastWebhookAt.UTC())

	resolved, err := resolveRepositoryForRefresh(ctx, db, &tracked, "")
	require.NoError(t, err)
	require.NotNil(t, resolved)
	require.Equal(t, repo.ID, resolved.ID)

	resolved, err = resolveRepositoryForRefresh(ctx, db, nil, "acme/widgets")
	require.NoError(t, err)
	require.NotNil(t, resolved)

	job := database.RepositoryRefreshJob{
		TrackedRepositoryID: &tracked.ID,
		RepositoryID:        &repo.ID,
		FullName:            "acme/widgets",
	}
	resolved, err = resolveRepositoryForJob(ctx, db, job)
	require.NoError(t, err)
	require.NotNil(t, resolved)

	require.Equal(t, map[string]any{"issues_completeness": completenessSparse}, CompletenessUpdatesForEvent("issues"))
	require.Equal(t, map[string]any{
		"issues_completeness":  completenessSparse,
		"pulls_completeness":   completenessSparse,
		"reviews_completeness": completenessSparse,
	}, CompletenessUpdatesForEvent("pull_request_review"))
	require.Empty(t, CompletenessUpdatesForEvent("push"))

	require.Equal(t, completenessBackfilled, mergeCompleteness(completenessSparse, completenessBackfilled))
	require.Equal(t, 3, completenessRank(completenessBackfilled))
	require.Equal(t, "widgets-renamed", firstNonEmpty("", " widgets-renamed "))
	require.Equal(t, 15*time.Second, backoffForAttempt(0))
	require.Equal(t, time.Minute, backoffForAttempt(2))
	require.Equal(t, 5*time.Minute, backoffForAttempt(10))

	older := seenAt
	newer := seenAt.Add(2 * time.Minute)
	require.Equal(t, &newer, laterTime(&older, &newer))
	require.NotNil(t, refreshJobIdentityCondition(db.WithContext(ctx), &tracked, &repo, "acme/widgets"))
}

func TestWorkerStartReturnsOnCanceledContext(t *testing.T) {
	db, err := database.Open("sqlite://" + filepath.Join(t.TempDir(), "refresh-start.db"))
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	worker := NewWorker(db, testBootstrapper(func(context.Context, string, string) error { return nil }), time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, worker.Start(ctx), context.Canceled)
}
