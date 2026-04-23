package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dutifuldev/ghreplica/internal/config"
	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/stretchr/testify/require"
)

func captureStandardStream(t *testing.T, current **os.File) (*os.File, func() string) {
	t.Helper()

	original := *current
	readPipe, writePipe, err := os.Pipe()
	require.NoError(t, err)
	*current = writePipe

	return original, func() string {
		require.NoError(t, writePipe.Close())
		*current = original
		body, err := io.ReadAll(readPipe)
		require.NoError(t, err)
		return string(body)
	}
}

func TestRunServeRejectsSQLiteWithBackgroundWebhookJobs(t *testing.T) {
	err := runServe(config.Config{
		DatabaseURL:   "sqlite://" + t.TempDir() + "/ghreplica.db",
		GitMirrorRoot: t.TempDir(),
		ASTGrepBin:    "/bin/true",
	})
	require.EqualError(t, err, "ghreplica serve requires PostgreSQL when background webhook jobs are enabled")
}

func TestRunBackfillAcceptsDocumentedArgumentOrder(t *testing.T) {
	err := runBackfill(config.Config{}, []string{"repo", "acme/widgets", "--mode", "open_only", "--priority", "10"})
	require.Error(t, err)
	require.EqualError(t, err, "DATABASE_URL is required")
}

func TestRunBackfillAcceptsFlagsBeforeTarget(t *testing.T) {
	err := runBackfill(config.Config{}, []string{"--mode", "open_only", "--priority", "10", "repo", "acme/widgets"})
	require.Error(t, err)
	require.EqualError(t, err, "DATABASE_URL is required")
}

func TestRunBackfillAcceptsNewRepairModes(t *testing.T) {
	for _, mode := range []string{"open_and_recent", "full_history"} {
		err := runBackfill(config.Config{}, []string{"repo", "acme/widgets", "--mode", mode})
		require.Error(t, err)
		require.EqualError(t, err, "DATABASE_URL is required")
	}
}

func TestRunRepairAcceptsDocumentedArguments(t *testing.T) {
	err := runRepair(config.Config{}, []string{"recent", "repo", "acme/widgets"})
	require.Error(t, err)
	require.EqualError(t, err, "DATABASE_URL is required")
}

func TestRunRefreshInventoryAcceptsDocumentedArguments(t *testing.T) {
	err := runRefresh(config.Config{}, []string{"inventory", "repo", "acme/widgets"})
	require.Error(t, err)
	require.EqualError(t, err, "DATABASE_URL is required")
}

func TestRunRefreshInventoryExecutesDirectScan(t *testing.T) {
	ctx := context.Background()
	dbURL := "sqlite://" + filepath.Join(t.TempDir(), "ghreplica.db")

	db, err := database.Open(dbURL)
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	repository := database.Repository{
		GitHubID:   101,
		OwnerLogin: "acme",
		Name:       "widgets",
		FullName:   "acme/widgets",
	}
	require.NoError(t, db.Create(&repository).Error)

	githubServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/acme/widgets/pulls" {
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode([]map[string]any{
				{
					"id":         2001,
					"node_id":    "PR_kwDOAAAB",
					"number":     12,
					"state":      "open",
					"title":      "Refresh inventory",
					"html_url":   "https://github.com/acme/widgets/pull/12",
					"url":        "https://api.github.test/repos/acme/widgets/pulls/12",
					"issue_url":  "https://api.github.test/repos/acme/widgets/issues/12",
					"diff_url":   "https://github.com/acme/widgets/pull/12.diff",
					"patch_url":  "https://github.com/acme/widgets/pull/12.patch",
					"created_at": "2026-04-22T10:00:00Z",
					"updated_at": "2026-04-22T10:05:00Z",
					"head": map[string]any{
						"label": "acme:refresh-inventory",
						"ref":   "refresh-inventory",
						"sha":   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
						"repo":  nil,
					},
					"base": map[string]any{
						"label": "acme:main",
						"ref":   "main",
						"sha":   "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
						"repo":  nil,
					},
					"user": map[string]any{
						"id":         3001,
						"login":      "alice",
						"avatar_url": "https://avatars.githubusercontent.com/u/3001?v=4",
						"html_url":   "https://github.com/alice",
						"url":        "https://api.github.test/users/alice",
						"type":       "User",
						"site_admin": false,
					},
					"draft": false,
				},
			}))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(githubServer.Close)

	cfg := config.Config{
		DatabaseURL:        dbURL,
		GitHubBaseURL:      githubServer.URL,
		GitMirrorRoot:      t.TempDir(),
		SyncDBMaxOpenConns: 1,
		SyncDBMaxIdleConns: 1,
		RepoLeaseTTL:       time.Minute,
	}

	originalStdout := os.Stdout
	readPipe, writePipe, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = writePipe
	t.Cleanup(func() {
		os.Stdout = originalStdout
	})

	err = runRefreshInventory(cfg, "acme/widgets")
	require.NoError(t, err)
	require.NoError(t, writePipe.Close())

	output, err := io.ReadAll(readPipe)
	require.NoError(t, err)
	require.Contains(t, string(output), "refresh inventory repo=acme/widgets")
	require.Contains(t, string(output), "generation=1")

	var state database.RepoChangeSyncState
	require.NoError(t, db.WithContext(ctx).Where("repository_id = ?", repository.ID).First(&state).Error)
	require.Equal(t, 1, state.InventoryGenerationCurrent)
	require.Equal(t, 1, state.OpenPRTotal)
	require.NotNil(t, state.InventoryLastCommittedAt)
}

func TestRunSearchIndexAcceptsDocumentedArguments(t *testing.T) {
	err := runSearchIndex(config.Config{}, []string{"repo", "acme/widgets"})
	require.Error(t, err)
	require.EqualError(t, err, "DATABASE_URL is required")
}

func TestRunCleanupAcceptsDocumentedArgumentOrder(t *testing.T) {
	err := runCleanup(config.Config{}, []string{"webhook-deliveries", "--until-empty"})
	require.Error(t, err)
	require.EqualError(t, err, "DATABASE_URL is required")
}

func TestRunCleanupAcceptsFlagsBeforeTarget(t *testing.T) {
	err := runCleanup(config.Config{}, []string{"--until-empty", "webhook-deliveries"})
	require.Error(t, err)
	require.EqualError(t, err, "DATABASE_URL is required")
}

func TestRunUsageHelpersAndValidation(t *testing.T) {
	err := run(nil)
	require.EqualError(t, err, "invalid command")

	require.EqualError(t, run([]string{"bogus"}), "invalid command")
	require.EqualError(t, runMigrate(config.Config{}, nil), "DATABASE_URL is required")
	require.EqualError(t, runSync(config.Config{}, nil), "usage: ghreplica sync {repo <owner>/<repo> | issue <owner>/<repo> <number> | pr <owner>/<repo> <number>}")
	require.EqualError(t, runRefresh(config.Config{}, nil), "usage: ghreplica refresh {repo <owner>/<repo> | inventory repo <owner>/<repo>}")
	require.EqualError(t, runRepair(config.Config{}, nil), "usage: ghreplica repair recent repo <owner>/<repo>")
	require.EqualError(t, runSearchIndex(config.Config{}, nil), "usage: ghreplica search-index repo <owner>/<repo>")

	number, err := parseNumberArg("42")
	require.NoError(t, err)
	require.Equal(t, 42, number)
	_, err = parseNumberArg("0")
	require.EqualError(t, err, `invalid number: "0"`)
	require.Equal(t, "", formatTimePtr(nil))

	originalStderr, restore := captureStandardStream(t, &os.Stderr)
	defer func() { os.Stderr = originalStderr }()
	err = usageError()
	require.EqualError(t, err, "invalid command")
	output := restore()
	require.Contains(t, output, "ghreplica serve")
	require.Contains(t, output, "ghreplica sync pr <owner>/<repo> <number>")
}

func TestRunCleanupAndSearchIndexExecuteWithSQLite(t *testing.T) {
	ctx := context.Background()
	dbURL := "sqlite://" + filepath.Join(t.TempDir(), "cleanup.db")
	db, err := database.Open(dbURL)
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	receivedAt := time.Now().UTC().Add(-2 * time.Hour)
	processedAt := receivedAt.Add(time.Minute)
	require.NoError(t, db.WithContext(ctx).Create(&database.WebhookDelivery{
		DeliveryID:  "cleanup-1",
		Event:       "ping",
		ReceivedAt:  receivedAt,
		ProcessedAt: &processedAt,
	}).Error)

	cfg := config.Config{
		DatabaseURL:                     dbURL,
		WebhookDeliveryRetention:        time.Hour,
		WebhookDeliveryCleanupInterval:  time.Second,
		WebhookDeliveryCleanupBatchSize: 10,
	}

	originalStdout, restoreStdout := captureStandardStream(t, &os.Stdout)
	defer func() { os.Stdout = originalStdout }()
	require.NoError(t, runCleanup(cfg, []string{"webhook-deliveries", "--until-empty"}))
	output := restoreStdout()
	require.Contains(t, output, "cleanup webhook-deliveries")

	var deliveries int64
	require.NoError(t, db.WithContext(ctx).Model(&database.WebhookDelivery{}).Count(&deliveries).Error)
	require.Zero(t, deliveries)

	repo := database.Repository{
		GitHubID:   100,
		OwnerLogin: "acme",
		Name:       "widgets",
		FullName:   "acme/widgets",
	}
	require.NoError(t, db.WithContext(ctx).Create(&repo).Error)
	require.NoError(t, runSearchIndex(config.Config{DatabaseURL: dbURL}, []string{"repo", "acme/widgets"}))
}

func TestRunRefreshEnqueuesManualJob(t *testing.T) {
	ctx := context.Background()
	dbURL := "sqlite://" + filepath.Join(t.TempDir(), "refresh.db")
	db, err := database.Open(dbURL)
	require.NoError(t, err)
	require.NoError(t, database.ApplyTestSchema(db))

	cfg := config.Config{DatabaseURL: dbURL}
	require.NoError(t, runRefresh(cfg, []string{"repo", "acme/widgets"}))

	var job database.RepositoryRefreshJob
	require.NoError(t, db.WithContext(ctx).First(&job).Error)
	require.Equal(t, "manual", job.Source)
	require.Equal(t, "acme/widgets", job.FullName)
}

func TestRunSyncSubcommandsValidateArguments(t *testing.T) {
	dbURL := "sqlite://" + filepath.Join(t.TempDir(), "sync.db")
	cfg := config.Config{
		DatabaseURL:          dbURL,
		DatabaseMaxOpenConns: 1,
		DatabaseMaxIdleConns: 1,
		GitMirrorRoot:        t.TempDir(),
		ASTGrepBin:           "/bin/true",
	}

	err := runSync(cfg, []string{"repo", "bad-format"})
	require.EqualError(t, err, "repository must be in owner/repo form")

	err = runSync(cfg, []string{"issue", "acme/widgets", "nope"})
	require.EqualError(t, err, `invalid number: "nope"`)

	err = runSync(cfg, []string{"pr", "acme/widgets", "0"})
	require.EqualError(t, err, `invalid number: "0"`)

	err = runSync(cfg, []string{"wat", "acme/widgets"})
	require.EqualError(t, err, "usage: ghreplica sync {repo <owner>/<repo> | issue <owner>/<repo> <number> | pr <owner>/<repo> <number>}")
}
