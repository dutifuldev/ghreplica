package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/dutifuldev/ghreplica/internal/httpapi"
	"github.com/stretchr/testify/require"
)

func TestReadinessIgnoresHistoricalFailedJobsAndSupersededJobs(t *testing.T) {
	ctx := context.Background()

	db, err := database.Open("sqlite://file::memory:?cache=shared")
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	oldFailedAt := time.Now().UTC().Add(-2 * time.Hour)
	require.NoError(t, db.WithContext(ctx).Create(&database.RepositoryRefreshJob{
		FullName:    "acme/widgets",
		JobType:     "bootstrap_repository",
		Source:      "manual",
		Status:      "failed",
		MaxAttempts: 3,
		RequestedAt: oldFailedAt,
		FinishedAt:  &oldFailedAt,
		CreatedAt:   oldFailedAt,
		UpdatedAt:   oldFailedAt,
	}).Error)
	require.NoError(t, db.WithContext(ctx).Create(&database.RepositoryRefreshJob{
		FullName:    "acme/widgets",
		JobType:     "bootstrap_repository",
		Source:      "webhook",
		Status:      "superseded",
		MaxAttempts: 3,
		RequestedAt: oldFailedAt,
		FinishedAt:  &oldFailedAt,
		CreatedAt:   oldFailedAt,
		UpdatedAt:   oldFailedAt,
	}).Error)

	server := httpapi.NewServer(db, httpapi.Options{})
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	server.Echo().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Equal(t, "ready", payload["status"])
	require.EqualValues(t, 0, payload["recent_failed_jobs"])
	require.EqualValues(t, 1, payload["failed_jobs_total"])
	require.EqualValues(t, 1, payload["superseded_jobs"])
}
