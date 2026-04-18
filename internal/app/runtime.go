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

type ServeRuntime struct {
	DB               *gorm.DB
	SQLDB            *sql.DB
	GitHubClient     *github.Client
	GitIndex         *gitindex.Service
	GitHubSync       *githubsync.Service
	WebhookIngestor  *webhooks.Service
	WebhookJobClient *river.Client[*sql.Tx]
	RefreshWorker    *refresh.Worker
	ChangeSyncWorker *githubsync.ChangeSyncWorker
	Server           *httpapi.Server
}

func OpenDatabase(cfg config.Config) (*gorm.DB, error) {
	return database.OpenWithPoolConfig(cfg.DatabaseURL, database.PoolConfig{
		MaxOpenConns: cfg.DatabaseMaxOpenConns,
		MaxIdleConns: cfg.DatabaseMaxIdleConns,
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
	db, err := OpenDatabase(cfg)
	if err != nil {
		return nil, err
	}

	githubClient := NewGitHubClient(cfg)
	gitIndex := NewGitIndexService(db, githubClient, cfg)
	githubSync := githubsync.NewService(db, githubClient, gitIndex)
	webhookIngestor := webhooks.NewService(db, webhooks.Dependencies{
		Projector: githubSync,
		Staler:    githubSync,
		Recorder:  githubSync,
	})

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	webhookJobClient, dispatcher, err := webhookjobs.NewClient(sqlDB, webhookIngestor, webhookjobs.Config{
		QueueConcurrency: cfg.WebhookJobQueueConcurrency,
		JobTimeout:       cfg.WebhookJobTimeout,
		MaxAttempts:      cfg.WebhookJobMaxAttempts,
	})
	if err != nil {
		return nil, err
	}
	webhookIngestor.SetDispatcher(dispatcher)

	return &ServeRuntime{
		DB:               db,
		SQLDB:            sqlDB,
		GitHubClient:     githubClient,
		GitIndex:         gitIndex,
		GitHubSync:       githubSync,
		WebhookIngestor:  webhookIngestor,
		WebhookJobClient: webhookJobClient,
		RefreshWorker:    refresh.NewWorker(db, githubSync, 2*time.Second),
		ChangeSyncWorker: githubsync.NewChangeSyncWorker(
			db,
			githubSync,
			cfg.ChangeSyncPollInterval,
			cfg.WebhookFetchDebounce,
			cfg.OpenPRInventoryMaxAge,
			cfg.RepoLeaseTTL,
			cfg.BackfillMaxRuntime,
			cfg.BackfillMaxPRsPerPass,
		),
		Server: httpapi.NewServer(db, httpapi.Options{
			GitHubWebhookSecret: cfg.GitHubWebhookSecret,
			WebhookIngestor:     webhookIngestor,
			ChangeStatus:        githubSync,
			StructuralSearch:    gitIndex,
		}),
	}, nil
}
