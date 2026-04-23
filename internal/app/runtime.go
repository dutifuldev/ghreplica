package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/dutifuldev/ghreplica/internal/config"
	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/dutifuldev/ghreplica/internal/github"
	"github.com/dutifuldev/ghreplica/internal/githubsync"
	"github.com/dutifuldev/ghreplica/internal/gitindex"
	"github.com/dutifuldev/ghreplica/internal/httpapi"
	"github.com/dutifuldev/ghreplica/internal/refresh"
	"github.com/dutifuldev/ghreplica/internal/webhookjobs"
	"github.com/dutifuldev/ghreplica/internal/webhooks"
	"github.com/riverqueue/river"
	"gorm.io/gorm"
)

type ServeRuntime struct {
	ControlDB            *gorm.DB
	ControlSQLDB         *sql.DB
	WebhookDB            *gorm.DB
	WebhookSQLDB         *sql.DB
	QueueDB              *gorm.DB
	QueueSQLDB           *sql.DB
	SyncDB               *gorm.DB
	SyncSQLDB            *sql.DB
	GitHubClient         *github.Client
	SyncGitIndex         *gitindex.Service
	ControlGitHubSync    *githubsync.Service
	SyncGitHubSync       *githubsync.Service
	WebhookIngestor      *webhooks.Service
	WebhookJobClient     *river.Client[*sql.Tx]
	WebhookCleanupWorker *webhooks.DeliveryCleanupWorker
	RefreshWorker        *refresh.Worker
	ChangeSyncWorker     *githubsync.ChangeSyncWorker
	Server               *httpapi.Server
	connectorCleanup     func() error
}

type OpenedDatabase struct {
	DB      *gorm.DB
	SQLDB   *sql.DB
	cleanup func() error
}

func (d *OpenedDatabase) Close() error {
	if d == nil {
		return nil
	}
	var errs []error
	if d.SQLDB != nil {
		errs = append(errs, d.SQLDB.Close())
	}
	if d.cleanup != nil {
		errs = append(errs, d.cleanup())
	}
	return errors.Join(errs...)
}

func newDatabaseConnector(cfg config.Config) (*database.Connector, error) {
	return database.NewConnector(database.ConnectConfig{
		DatabaseURL:                    cfg.DatabaseURL,
		Dialer:                         cfg.DatabaseDialer,
		CloudSQLInstanceConnectionName: cfg.CloudSQLInstanceConnectionName,
		CloudSQLUseIAMAuthN:            cfg.CloudSQLUseIAMAuthN,
	})
}

func OpenDatabaseHandle(cfg config.Config) (*OpenedDatabase, error) {
	connector, err := newDatabaseConnector(cfg)
	if err != nil {
		return nil, err
	}
	handle, err := connector.Open(database.PoolConfig{
		MaxOpenConns: cfg.DatabaseMaxOpenConns,
		MaxIdleConns: cfg.DatabaseMaxIdleConns,
	})
	if err != nil {
		_ = connector.Close()
		return nil, err
	}
	return &OpenedDatabase{
		DB:      handle.GormDB,
		SQLDB:   handle.SQLDB,
		cleanup: connector.Close,
	}, nil
}

func OpenWebhookDatabaseHandle(cfg config.Config) (*OpenedDatabase, error) {
	connector, err := newDatabaseConnector(cfg)
	if err != nil {
		return nil, err
	}
	handle, err := OpenWebhookDatabase(cfg, connector)
	if err != nil {
		_ = connector.Close()
		return nil, err
	}
	return &OpenedDatabase{
		DB:      handle.GormDB,
		SQLDB:   handle.SQLDB,
		cleanup: connector.Close,
	}, nil
}

func OpenSyncDatabaseHandle(cfg config.Config) (*OpenedDatabase, error) {
	connector, err := newDatabaseConnector(cfg)
	if err != nil {
		return nil, err
	}
	handle, err := OpenSyncDatabase(cfg, connector)
	if err != nil {
		_ = connector.Close()
		return nil, err
	}
	return &OpenedDatabase{
		DB:      handle.GormDB,
		SQLDB:   handle.SQLDB,
		cleanup: connector.Close,
	}, nil
}

func OpenControlDatabase(cfg config.Config, connector *database.Connector) (*database.Handle, error) {
	return connector.Open(database.PoolConfig{
		MaxOpenConns: cfg.ControlDBMaxOpenConns,
		MaxIdleConns: cfg.ControlDBMaxIdleConns,
	})
}

func OpenQueueDatabase(cfg config.Config, connector *database.Connector) (*database.Handle, error) {
	return connector.Open(database.PoolConfig{
		MaxOpenConns: cfg.QueueDBMaxOpenConns,
		MaxIdleConns: cfg.QueueDBMaxIdleConns,
	})
}

func OpenWebhookDatabase(cfg config.Config, connector *database.Connector) (*database.Handle, error) {
	return connector.Open(database.PoolConfig{
		MaxOpenConns: cfg.WebhookDBMaxOpenConns,
		MaxIdleConns: cfg.WebhookDBMaxIdleConns,
	})
}

func OpenSyncDatabase(cfg config.Config, connector *database.Connector) (*database.Handle, error) {
	return connector.Open(database.PoolConfig{
		MaxOpenConns: cfg.SyncDBMaxOpenConns,
		MaxIdleConns: cfg.SyncDBMaxIdleConns,
	})
}

func prewarmServeRuntimePools(cfg config.Config, controlSQLDB, webhookSQLDB, queueSQLDB, syncSQLDB *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	for _, pool := range []struct {
		name   string
		sqlDB  *sql.DB
		target int
	}{
		{name: "control", sqlDB: controlSQLDB, target: min(2, cfg.ControlDBMaxIdleConns)},
		{name: "webhook", sqlDB: webhookSQLDB, target: min(1, cfg.WebhookDBMaxIdleConns)},
		{name: "queue", sqlDB: queueSQLDB, target: min(2, cfg.QueueDBMaxIdleConns)},
		{name: "sync", sqlDB: syncSQLDB, target: min(2, cfg.SyncDBMaxIdleConns)},
	} {
		if err := database.PrewarmPool(ctx, pool.sqlDB, pool.target); err != nil {
			return fmt.Errorf("prewarm %s database pool: %w", pool.name, err)
		}
	}

	return nil
}

func NewGitHubClient(cfg config.Config) *github.Client {
	return github.NewClient(cfg.GitHubBaseURL, github.AuthConfig{
		Token:          cfg.GitHubToken,
		AppID:          cfg.GitHubAppID,
		InstallationID: cfg.GitHubInstallationID,
		PrivateKeyPEM:  cfg.GitHubAppPrivateKeyPEM,
		PrivateKeyPath: cfg.GitHubAppPrivateKeyPath,
	})
}

func NewGitIndexService(db *gorm.DB, client *github.Client, cfg config.Config) *gitindex.Service {
	return gitindex.NewService(db, client, cfg.GitMirrorRoot).
		WithIndexTimeout(cfg.GitIndexTimeout).
		WithASTGrepBinary(cfg.ASTGrepBin).
		WithASTGrepTimeout(cfg.ASTGrepTimeout)
}

func NewServeRuntime(cfg config.Config) (*ServeRuntime, error) {
	connector, err := newDatabaseConnector(cfg)
	if err != nil {
		return nil, err
	}
	controlHandle, err := OpenControlDatabase(cfg, connector)
	if err != nil {
		_ = connector.Close()
		return nil, err
	}
	queueHandle, err := OpenQueueDatabase(cfg, connector)
	if err != nil {
		_ = controlHandle.SQLDB.Close()
		_ = connector.Close()
		return nil, err
	}
	webhookHandle, err := OpenWebhookDatabase(cfg, connector)
	if err != nil {
		_ = queueHandle.SQLDB.Close()
		_ = controlHandle.SQLDB.Close()
		_ = connector.Close()
		return nil, err
	}
	syncHandle, err := OpenSyncDatabase(cfg, connector)
	if err != nil {
		_ = webhookHandle.SQLDB.Close()
		_ = queueHandle.SQLDB.Close()
		_ = controlHandle.SQLDB.Close()
		_ = connector.Close()
		return nil, err
	}
	controlDB := controlHandle.GormDB
	webhookDB := webhookHandle.GormDB
	queueDB := queueHandle.GormDB
	syncDB := syncHandle.GormDB

	githubClient := NewGitHubClient(cfg)
	syncGitIndex := NewGitIndexService(syncDB, githubClient, cfg)
	controlGitHubSync := githubsync.NewService(controlDB, githubClient).
		WithOpenPRInventoryMaxAge(cfg.OpenPRInventoryMaxAge)
	syncGitHubSync := githubsync.NewService(syncDB, githubClient, syncGitIndex).
		WithOpenPRInventoryMaxAge(cfg.OpenPRInventoryMaxAge)
	webhookIngestor := webhooks.NewService(webhookDB, syncDB, webhooks.Dependencies{
		Projector: syncGitHubSync,
		Staler:    syncGitHubSync,
		Recorder:  syncGitHubSync,
		ImmediatePullRequestProjectorFactory: func(tx *gorm.DB) webhooks.ImmediatePullRequestProjector {
			return githubsync.NewService(tx, githubClient).
				WithOpenPRInventoryMaxAge(cfg.OpenPRInventoryMaxAge).
				WithoutSearch()
		},
	})

	controlSQLDB := controlHandle.SQLDB
	webhookSQLDB := webhookHandle.SQLDB
	queueSQLDB := queueHandle.SQLDB
	syncSQLDB := syncHandle.SQLDB
	if err := prewarmServeRuntimePools(cfg, controlSQLDB, webhookSQLDB, queueSQLDB, syncSQLDB); err != nil {
		_ = syncSQLDB.Close()
		_ = webhookSQLDB.Close()
		_ = queueSQLDB.Close()
		_ = controlSQLDB.Close()
		_ = connector.Close()
		return nil, err
	}
	webhookJobClient, dispatcher, err := webhookjobs.NewClient(queueSQLDB, webhookIngestor, webhookjobs.Config{
		QueueConcurrency: cfg.WebhookJobQueueConcurrency,
		JobTimeout:       cfg.WebhookJobTimeout,
		MaxAttempts:      cfg.WebhookJobMaxAttempts,
	})
	if err != nil {
		_ = syncSQLDB.Close()
		_ = webhookSQLDB.Close()
		_ = queueSQLDB.Close()
		_ = controlSQLDB.Close()
		_ = connector.Close()
		return nil, err
	}
	webhookIngestor.SetDispatcher(dispatcher)

	return &ServeRuntime{
		ControlDB:            controlDB,
		ControlSQLDB:         controlSQLDB,
		WebhookDB:            webhookDB,
		WebhookSQLDB:         webhookSQLDB,
		QueueDB:              queueDB,
		QueueSQLDB:           queueSQLDB,
		SyncDB:               syncDB,
		SyncSQLDB:            syncSQLDB,
		GitHubClient:         githubClient,
		SyncGitIndex:         syncGitIndex,
		ControlGitHubSync:    controlGitHubSync,
		SyncGitHubSync:       syncGitHubSync,
		WebhookIngestor:      webhookIngestor,
		WebhookJobClient:     webhookJobClient,
		WebhookCleanupWorker: webhooks.NewDeliveryCleanupWorker(webhookDB, cfg.WebhookDeliveryRetention, cfg.WebhookDeliveryCleanupInterval, cfg.WebhookDeliveryCleanupBatchSize),
		RefreshWorker:        refresh.NewWorker(syncDB, syncGitHubSync, 2*time.Second),
		ChangeSyncWorker: githubsync.NewChangeSyncWorker(
			syncDB,
			syncGitHubSync,
			cfg.ChangeSyncPollInterval,
			cfg.WebhookFetchDebounce,
			cfg.OpenPRInventoryMaxAge,
			cfg.RepoLeaseTTL,
			cfg.BackfillMaxRuntime,
			cfg.BackfillMaxPRsPerPass,
		),
		Server: httpapi.NewServer(controlDB, httpapi.Options{
			GitHubWebhookSecret: cfg.GitHubWebhookSecret,
			WebhookIngestor:     webhookIngestor,
			ChangeStatus:        controlGitHubSync,
			StructuralSearch:    syncGitIndex,
		}),
		connectorCleanup: connector.Close,
	}, nil
}

func (r *ServeRuntime) Close() error {
	if r == nil {
		return nil
	}
	var errs []error
	if r.SyncSQLDB != nil {
		errs = append(errs, r.SyncSQLDB.Close())
	}
	if r.WebhookSQLDB != nil {
		errs = append(errs, r.WebhookSQLDB.Close())
	}
	if r.QueueSQLDB != nil {
		errs = append(errs, r.QueueSQLDB.Close())
	}
	if r.ControlSQLDB != nil {
		errs = append(errs, r.ControlSQLDB.Close())
	}
	if r.connectorCleanup != nil {
		errs = append(errs, r.connectorCleanup())
	}
	return errors.Join(errs...)
}
