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

	"github.com/dutifuldev/ghreplica/internal/config"
	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/dutifuldev/ghreplica/internal/github"
	"github.com/dutifuldev/ghreplica/internal/httpapi"
	syncsvc "github.com/dutifuldev/ghreplica/internal/sync"
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

	server := httpapi.NewServer(db)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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

	client := github.NewClient(cfg.GitHubBaseURL, cfg.GitHubToken)
	service := syncsvc.NewService(db, client)

	return service.BootstrapRepository(context.Background(), owner, repo)
}

func usageError() error {
	fmt.Fprintf(os.Stderr, "usage:\n")
	fmt.Fprintf(os.Stderr, "  ghreplica serve\n")
	fmt.Fprintf(os.Stderr, "  ghreplica migrate up\n")
	fmt.Fprintf(os.Stderr, "  ghreplica sync repo <owner>/<repo>\n")
	return errors.New("invalid command")
}
