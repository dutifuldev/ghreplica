package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dutifuldev/ghreplica/internal/config"
	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/dutifuldev/ghreplica/internal/github"
	"github.com/dutifuldev/ghreplica/internal/githubsync"
	"github.com/dutifuldev/ghreplica/internal/httpapi"
	"github.com/dutifuldev/ghreplica/internal/refresh"
	"github.com/dutifuldev/ghreplica/internal/webhooks"
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
	case "refresh":
		return runRefresh(cfg, args[1:])
	case "sync":
		return runSync(cfg, args[1:])
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

	githubSync := githubsync.NewService(db, github.NewClient(cfg.GitHubBaseURL, github.AuthConfig{
		Token:          cfg.GitHubToken,
		AppID:          cfg.GitHubAppID,
		InstallationID: cfg.GitHubInstallationID,
		PrivateKeyPEM:  cfg.GitHubAppPrivateKeyPEM,
		PrivateKeyPath: cfg.GitHubAppPrivateKeyPath,
	}))
	webhookIngestor := webhooks.NewService(db, githubSync)
	worker := refresh.NewWorker(db, githubSync, 2*time.Second)

	server := httpapi.NewServer(db, httpapi.Options{
		GitHubWebhookSecret: cfg.GitHubWebhookSecret,
		WebhookIngestor:     webhookIngestor,
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := worker.Start(ctx); err != nil && ctx.Err() == nil {
			slog.Error("refresh worker stopped", "error", err)
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
	if len(rest) != 2 || rest[0] != "repo" {
		return errors.New("usage: ghreplica sync repo <owner>/<repo>")
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
	service := githubsync.NewService(db, client)

	return service.BootstrapRepository(context.Background(), owner, repo)
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

func usageError() error {
	fmt.Fprintf(os.Stderr, "usage:\n")
	fmt.Fprintf(os.Stderr, "  ghreplica serve\n")
	fmt.Fprintf(os.Stderr, "  ghreplica migrate up\n")
	fmt.Fprintf(os.Stderr, "  ghreplica refresh repo <owner>/<repo>\n")
	fmt.Fprintf(os.Stderr, "  ghreplica sync repo <owner>/<repo>\n")
	return errors.New("invalid command")
}
