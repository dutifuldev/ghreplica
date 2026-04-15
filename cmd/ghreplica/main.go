package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/dutifuldev/ghreplica/internal/config"
	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/dutifuldev/ghreplica/internal/github"
	"github.com/dutifuldev/ghreplica/internal/githubsync"
	"github.com/dutifuldev/ghreplica/internal/gitindex"
	"github.com/dutifuldev/ghreplica/internal/httpapi"
	"github.com/dutifuldev/ghreplica/internal/refresh"
	"github.com/dutifuldev/ghreplica/internal/searchindex"
	"github.com/dutifuldev/ghreplica/internal/webhooks"
	"gorm.io/gorm"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("command failed", "error", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usageError()
	}

	cfg := config.Load()

	switch args[0] {
	case "serve":
		return runServe(cfg)
	case "migrate":
		return runMigrate(cfg, args[1:])
	case "backfill":
		return runBackfill(cfg, args[1:])
	case "refresh":
		return runRefresh(cfg, args[1:])
	case "sync":
		return runSync(cfg, args[1:])
	case "search-index":
		return runSearchIndex(cfg, args[1:])
	default:
		return usageError()
	}
}

func runServe(cfg config.Config) error {
	if err := cfg.ValidateDatabase(); err != nil {
		return err
	}

	db, err := database.Open(cfg.DatabaseURL)
	if err != nil {
		return err
	}

	githubClient := github.NewClient(cfg.GitHubBaseURL, github.AuthConfig{
		Token:          cfg.GitHubToken,
		AppID:          cfg.GitHubAppID,
		InstallationID: cfg.GitHubInstallationID,
		PrivateKeyPEM:  cfg.GitHubAppPrivateKeyPEM,
		PrivateKeyPath: cfg.GitHubAppPrivateKeyPath,
	})
	gitIndex := newGitIndexService(db, githubClient, cfg)
	githubSync := githubsync.NewService(db, githubClient, gitIndex)
	webhookIngestor := webhooks.NewService(db, githubSync)
	worker := refresh.NewWorker(db, githubSync, 2*time.Second)
	changeSyncWorker := githubsync.NewChangeSyncWorker(
		db,
		githubSync,
		cfg.ChangeSyncPollInterval,
		cfg.WebhookFetchDebounce,
		cfg.RepoMinFetchInterval,
		cfg.OpenPRBackfillInterval,
		cfg.RepoLeaseTTL,
		cfg.RepoBackfillMaxRuntime,
		cfg.RepoBackfillMaxPRs,
	)

	server := httpapi.NewServer(db, httpapi.Options{
		GitHubWebhookSecret: cfg.GitHubWebhookSecret,
		WebhookIngestor:     webhookIngestor,
		ChangeStatus:        githubSync,
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := worker.Start(ctx); err != nil && ctx.Err() == nil {
			slog.Error("refresh worker stopped", "error", err)
			stop()
		}
	}()
	go func() {
		if err := changeSyncWorker.Start(ctx); err != nil && ctx.Err() == nil {
			slog.Error("change sync worker stopped", "error", err)
			stop()
		}
	}()

	return server.Start(ctx, cfg.AppAddr)
}

func runMigrate(cfg config.Config, args []string) error {
	if err := cfg.ValidateDatabase(); err != nil {
		return err
	}

	if len(args) != 1 || args[0] != "up" {
		return errors.New("usage: ghreplica migrate up")
	}

	db, err := database.Open(cfg.DatabaseURL)
	if err != nil {
		return err
	}

	if database.IsSQLiteURL(cfg.DatabaseURL) {
		return database.AutoMigrate(db)
	}

	return database.RunMigrations(db, "migrations")
}

func runSync(cfg config.Config, args []string) error {
	syncFlags := flag.NewFlagSet("sync", flag.ContinueOnError)
	if err := syncFlags.Parse(args); err != nil {
		return err
	}

	rest := syncFlags.Args()
	if len(rest) < 2 {
		return errors.New("usage: ghreplica sync {repo <owner>/<repo> | issue <owner>/<repo> <number> | pr <owner>/<repo> <number>}")
	}
	if err := cfg.ValidateDatabase(); err != nil {
		return err
	}

	db, err := database.Open(cfg.DatabaseURL)
	if err != nil {
		return err
	}

	client := github.NewClient(cfg.GitHubBaseURL, github.AuthConfig{
		Token:          cfg.GitHubToken,
		AppID:          cfg.GitHubAppID,
		InstallationID: cfg.GitHubInstallationID,
		PrivateKeyPEM:  cfg.GitHubAppPrivateKeyPEM,
		PrivateKeyPath: cfg.GitHubAppPrivateKeyPath,
	})
	service := githubsync.NewService(db, client, newGitIndexService(db, client, cfg))

	switch rest[0] {
	case "repo":
		if len(rest) != 2 {
			return errors.New("usage: ghreplica sync repo <owner>/<repo>")
		}
		owner, repo, err := config.ParseFullName(rest[1])
		if err != nil {
			return err
		}
		return service.BootstrapRepository(context.Background(), owner, repo)
	case "issue":
		if len(rest) != 3 {
			return errors.New("usage: ghreplica sync issue <owner>/<repo> <number>")
		}
		owner, repo, err := config.ParseFullName(rest[1])
		if err != nil {
			return err
		}
		number, err := parseNumberArg(rest[2])
		if err != nil {
			return err
		}
		return service.SyncIssue(context.Background(), owner, repo, number)
	case "pr":
		if len(rest) != 3 {
			return errors.New("usage: ghreplica sync pr <owner>/<repo> <number>")
		}
		owner, repo, err := config.ParseFullName(rest[1])
		if err != nil {
			return err
		}
		number, err := parseNumberArg(rest[2])
		if err != nil {
			return err
		}
		return service.SyncPullRequest(context.Background(), owner, repo, number)
	default:
		return errors.New("usage: ghreplica sync {repo <owner>/<repo> | issue <owner>/<repo> <number> | pr <owner>/<repo> <number>}")
	}
}

func runRefresh(cfg config.Config, args []string) error {
	refreshFlags := flag.NewFlagSet("refresh", flag.ContinueOnError)
	if err := refreshFlags.Parse(args); err != nil {
		return err
	}

	rest := refreshFlags.Args()
	if len(rest) != 2 || rest[0] != "repo" {
		return errors.New("usage: ghreplica refresh repo <owner>/<repo>")
	}

	owner, repo, err := config.ParseFullName(rest[1])
	if err != nil {
		return err
	}
	if err := cfg.ValidateDatabase(); err != nil {
		return err
	}

	db, err := database.Open(cfg.DatabaseURL)
	if err != nil {
		return err
	}

	return refresh.NewScheduler(db).EnqueueRepositoryRefresh(context.Background(), refresh.Request{
		Owner:      owner,
		Name:       repo,
		FullName:   owner + "/" + repo,
		Source:     "manual",
		DeliveryID: "",
	})
}

func runBackfill(cfg config.Config, args []string) error {
	var targetArgs, flagArgs []string
	if len(args) >= 2 && args[0] == "repo" {
		targetArgs = args[:2]
		flagArgs = args[2:]
	} else {
		flagArgs = args
	}

	backfillFlags := flag.NewFlagSet("backfill", flag.ContinueOnError)
	mode := backfillFlags.String("mode", "open_only", "backfill mode")
	priority := backfillFlags.Int("priority", 0, "backfill priority")
	if err := backfillFlags.Parse(flagArgs); err != nil {
		return err
	}

	rest := append(targetArgs, backfillFlags.Args()...)
	if len(rest) != 2 || rest[0] != "repo" {
		return errors.New("usage: ghreplica backfill repo <owner>/<repo> [--mode open_only] [--priority N]")
	}

	owner, repo, err := config.ParseFullName(rest[1])
	if err != nil {
		return err
	}
	if err := cfg.ValidateDatabase(); err != nil {
		return err
	}

	db, err := database.Open(cfg.DatabaseURL)
	if err != nil {
		return err
	}

	client := github.NewClient(cfg.GitHubBaseURL, github.AuthConfig{
		Token:          cfg.GitHubToken,
		AppID:          cfg.GitHubAppID,
		InstallationID: cfg.GitHubInstallationID,
		PrivateKeyPEM:  cfg.GitHubAppPrivateKeyPEM,
		PrivateKeyPath: cfg.GitHubAppPrivateKeyPath,
	})
	service := githubsync.NewService(db, client, newGitIndexService(db, client, cfg))
	_, err = service.ConfigureRepoBackfill(context.Background(), owner, repo, *mode, *priority)
	return err
}

func runSearchIndex(cfg config.Config, args []string) error {
	searchFlags := flag.NewFlagSet("search-index", flag.ContinueOnError)
	if err := searchFlags.Parse(args); err != nil {
		return err
	}

	rest := searchFlags.Args()
	if len(rest) != 2 || rest[0] != "repo" {
		return errors.New("usage: ghreplica search-index repo <owner>/<repo>")
	}
	if err := cfg.ValidateDatabase(); err != nil {
		return err
	}

	owner, repo, err := config.ParseFullName(rest[1])
	if err != nil {
		return err
	}

	db, err := database.Open(cfg.DatabaseURL)
	if err != nil {
		return err
	}

	return searchindex.NewService(db).RebuildRepository(context.Background(), owner, repo)
}

func newGitIndexService(db *gorm.DB, client *github.Client, cfg config.Config) *gitindex.Service {
	return gitindex.NewService(db, client, cfg.GitMirrorRoot).WithIndexTimeout(cfg.GitIndexTimeout)
}

func usageError() error {
	fmt.Fprintf(os.Stderr, "usage:\n")
	fmt.Fprintf(os.Stderr, "  ghreplica serve\n")
	fmt.Fprintf(os.Stderr, "  ghreplica migrate up\n")
	fmt.Fprintf(os.Stderr, "  ghreplica backfill repo <owner>/<repo> [--mode open_only] [--priority N]\n")
	fmt.Fprintf(os.Stderr, "  ghreplica refresh repo <owner>/<repo>\n")
	fmt.Fprintf(os.Stderr, "  ghreplica search-index repo <owner>/<repo>\n")
	fmt.Fprintf(os.Stderr, "  ghreplica sync repo <owner>/<repo>\n")
	fmt.Fprintf(os.Stderr, "  ghreplica sync issue <owner>/<repo> <number>\n")
	fmt.Fprintf(os.Stderr, "  ghreplica sync pr <owner>/<repo> <number>\n")
	return errors.New("invalid command")
}

func parseNumberArg(raw string) (int, error) {
	number, err := strconv.Atoi(raw)
	if err != nil || number <= 0 {
		return 0, fmt.Errorf("invalid number: %q", raw)
	}
	return number, nil
}
