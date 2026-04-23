package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/dutifuldev/ghreplica/internal/config"
	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/stretchr/testify/require"
)

func TestOpenedDatabaseCloseAndRuntimeHelpers(t *testing.T) {
	var nilHandle *OpenedDatabase
	require.NoError(t, nilHandle.Close())

	sqliteURL := "sqlite://" + filepath.Join(t.TempDir(), "ghreplica.db")
	cfg := config.Config{
		DatabaseURL:           sqliteURL,
		DatabaseMaxOpenConns:  1,
		DatabaseMaxIdleConns:  1,
		ControlDBMaxOpenConns: 1,
		ControlDBMaxIdleConns: 1,
		WebhookDBMaxOpenConns: 1,
		WebhookDBMaxIdleConns: 1,
		QueueDBMaxOpenConns:   1,
		QueueDBMaxIdleConns:   1,
		SyncDBMaxOpenConns:    1,
		SyncDBMaxIdleConns:    1,
		GitMirrorRoot:         t.TempDir(),
		ASTGrepBin:            "/bin/true",
		GitIndexTimeout:       5 * time.Second,
		ASTGrepTimeout:        3 * time.Second,
		GitHubBaseURL:         "https://api.github.test/",
		GitHubToken:           " secret-token ",
	}

	handle, err := OpenDatabaseHandle(cfg)
	require.NoError(t, err)
	require.NoError(t, handle.Close())

	webhookHandle, err := OpenWebhookDatabaseHandle(cfg)
	require.NoError(t, err)
	require.NoError(t, webhookHandle.Close())

	syncHandle, err := OpenSyncDatabaseHandle(cfg)
	require.NoError(t, err)
	require.NoError(t, syncHandle.Close())

	connector, err := newDatabaseConnector(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connector.Close()) })

	controlHandle, err := OpenControlDatabase(cfg, connector)
	require.NoError(t, err)
	queueHandle, err := OpenQueueDatabase(cfg, connector)
	require.NoError(t, err)
	webhookDBHandle, err := OpenWebhookDatabase(cfg, connector)
	require.NoError(t, err)
	syncDBHandle, err := OpenSyncDatabase(cfg, connector)
	require.NoError(t, err)

	require.NoError(t, prewarmServeRuntimePools(
		cfg,
		controlHandle.SQLDB,
		webhookDBHandle.SQLDB,
		queueHandle.SQLDB,
		syncDBHandle.SQLDB,
	))

	client := NewGitHubClient(cfg)
	token, err := client.AuthorizationToken(context.Background())
	require.NoError(t, err)
	require.Equal(t, "secret-token", token)

	indexService := NewGitIndexService(syncDBHandle.GormDB, client, cfg)
	require.NotNil(t, indexService)

	runtime := &ServeRuntime{
		ControlSQLDB: controlHandle.SQLDB,
		QueueSQLDB:   queueHandle.SQLDB,
		WebhookSQLDB: webhookDBHandle.SQLDB,
		SyncSQLDB:    syncDBHandle.SQLDB,
		connectorCleanup: func() error {
			return errors.New("cleanup failed")
		},
	}
	err = runtime.Close()
	require.Error(t, err)
	require.Contains(t, err.Error(), "cleanup failed")
}

func TestRunMigrateAndServeRejectSQLite(t *testing.T) {
	cfg := config.Config{
		DatabaseURL:          "sqlite://" + filepath.Join(t.TempDir(), "ghreplica.db"),
		GitMirrorRoot:        t.TempDir(),
		ASTGrepBin:           "/bin/true",
		DatabaseMaxOpenConns: 1,
		DatabaseMaxIdleConns: 1,
	}

	err := RunMigrate(cfg, nil)
	require.EqualError(t, err, "usage: ghreplica migrate up")

	err = RunMigrate(cfg, []string{"up"})
	require.EqualError(t, err, "ghreplica migrate requires PostgreSQL; SQLite schemas must be bootstrapped explicitly in tests with database.ApplyTestSchema")

	err = RunServe(cfg)
	require.EqualError(t, err, "ghreplica serve requires PostgreSQL when background webhook jobs are enabled")
}

func TestNewDatabaseConnectorRejectsUnsupportedDialer(t *testing.T) {
	_, err := newDatabaseConnector(config.Config{
		DatabaseURL:    "postgres://user@localhost/db?sslmode=disable",
		DatabaseDialer: "weird",
	})
	require.Error(t, err)
}

func TestOpenDatabaseHandleAppliesSQLiteSchema(t *testing.T) {
	cfg := config.Config{
		DatabaseURL:          "sqlite://" + filepath.Join(t.TempDir(), "schema.db"),
		DatabaseMaxOpenConns: 1,
		DatabaseMaxIdleConns: 1,
	}
	handle, err := OpenDatabaseHandle(cfg)
	require.NoError(t, err)
	defer func() { require.NoError(t, handle.Close()) }()

	require.NoError(t, database.ApplyTestSchema(handle.DB))
	require.True(t, handle.DB.Migrator().HasTable((&database.Repository{}).TableName()))
}
