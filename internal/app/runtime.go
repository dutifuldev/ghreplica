package app

import (
	"database/sql"
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

func closeDatabase(db *gorm.DB) {
	if db == nil {
		return
	}
	sqlDB, err := db.DB()
	if err == nil {
		_ = sqlDB.Close()
	}
}

type ServeRuntime struct {
	ControlDB         *gorm.DB
	ControlSQLDB      *sql.DB
	SyncDB            *gorm.DB
	SyncSQLDB         *sql.DB
	GitHubClient      *github.Client
	SyncGitIndex      *gitindex.Service
	ControlGitHubSync *githubsync.Service
	SyncGitHubSync    *githubsync.Service
	WebhookIngestor   *webhooks.Service
	WebhookJobClient  *river.Client[*sql.Tx]
	RefreshWorker     *refresh.Worker
	ChangeSyncWorker  *githubsync.ChangeSyncWorker
	Server            *httpapi.Server
}

func OpenDatabase(cfg config.Config) (*gorm.DB, error) {
	return database.OpenWithPoolConfig(cfg.DatabaseURL, database.PoolConfig{
		MaxOpenConns: cfg.DatabaseMaxOpenConns,
		MaxIdleConns: cfg.DatabaseMaxIdleConns,
	})
}

func OpenControlDatabase(cfg config.Config) (*gorm.DB, error) {
	return database.OpenWithPoolConfig(cfg.DatabaseURL, database.PoolConfig{
		MaxOpenConns: cfg.ControlDBMaxOpenConns,
		MaxIdleConns: cfg.ControlDBMaxIdleConns,
	})
}

func OpenSyncDatabase(cfg config.Config) (*gorm.DB, error) {
	return database.OpenWithPoolConfig(cfg.DatabaseURL, database.PoolConfig{
		MaxOpenConns: cfg.SyncDBMaxOpenConns,
		MaxIdleConns: cfg.SyncDBMaxIdleConns,
	})
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
	controlDB, err := OpenControlDatabase(cfg)
	if err != nil {
		return nil, err
	}
	syncDB, err := OpenSyncDatabase(cfg)
	if err != nil {
		closeDatabase(controlDB)
		return nil, err
	}

	githubClient := NewGitHubClient(cfg)
	syncGitIndex := NewGitIndexService(syncDB, githubClient, cfg)
	controlGitHubSync := githubsync.NewService(controlDB, githubClient)
	syncGitHubSync := githubsync.NewService(syncDB, githubClient, syncGitIndex)
	webhookIngestor := webhooks.NewService(controlDB, webhooks.Dependencies{
		Projector: controlGitHubSync,
		Staler:    controlGitHubSync,
		Recorder:  controlGitHubSync,
	})

	controlSQLDB, err := controlDB.DB()
	if err != nil {
		closeDatabase(syncDB)
		closeDatabase(controlDB)
		return nil, err
	}
	syncSQLDB, err := syncDB.DB()
	if err != nil {
		_ = controlSQLDB.Close()
		closeDatabase(syncDB)
		return nil, err
	}
	webhookJobClient, dispatcher, err := webhookjobs.NewClient(controlSQLDB, webhookIngestor, webhookjobs.Config{
		QueueConcurrency: cfg.WebhookJobQueueConcurrency,
		JobTimeout:       cfg.WebhookJobTimeout,
		MaxAttempts:      cfg.WebhookJobMaxAttempts,
	})
	if err != nil {
		_ = syncSQLDB.Close()
		_ = controlSQLDB.Close()
		return nil, err
	}
	webhookIngestor.SetDispatcher(dispatcher)

	return &ServeRuntime{
		ControlDB:         controlDB,
		ControlSQLDB:      controlSQLDB,
		SyncDB:            syncDB,
		SyncSQLDB:         syncSQLDB,
		GitHubClient:      githubClient,
		SyncGitIndex:      syncGitIndex,
		ControlGitHubSync: controlGitHubSync,
		SyncGitHubSync:    syncGitHubSync,
		WebhookIngestor:   webhookIngestor,
		WebhookJobClient:  webhookJobClient,
		RefreshWorker:     refresh.NewWorker(syncDB, syncGitHubSync, 2*time.Second),
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
	}, nil
}
