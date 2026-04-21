package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strconv"

	"github.com/dutifuldev/ghreplica/internal/app"
	"github.com/dutifuldev/ghreplica/internal/config"
	"github.com/dutifuldev/ghreplica/internal/githubsync"
	"github.com/dutifuldev/ghreplica/internal/refresh"
	"github.com/dutifuldev/ghreplica/internal/searchindex"
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
	case "backfill":
		return runBackfill(cfg, args[1:])
	case "refresh":
		return runRefresh(cfg, args[1:])
	case "repair":
		return runRepair(cfg, args[1:])
	case "cleanup":
		return runCleanup(cfg, args[1:])
	case "sync":
		return runSync(cfg, args[1:])
	case "search-index":
		return runSearchIndex(cfg, args[1:])
	default:
		return usageError()
	}
}

func runServe(cfg config.Config) error {
	return app.RunServe(cfg)
}

func runMigrate(cfg config.Config, args []string) error {
	return app.RunMigrate(cfg, args)
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

	dbHandle, err := app.OpenDatabaseHandle(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = dbHandle.Close() }()
	db := dbHandle.DB

	client := app.NewGitHubClient(cfg)
	service := githubsync.NewService(db, client, app.NewGitIndexService(db, client, cfg))

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

	dbHandle, err := app.OpenDatabaseHandle(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = dbHandle.Close() }()

	return refresh.NewScheduler(dbHandle.DB).EnqueueRepositoryRefresh(context.Background(), refresh.Request{
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
		return errors.New("usage: ghreplica backfill repo <owner>/<repo> [--mode open_only|open_and_recent|full_history] [--priority N]")
	}

	owner, repo, err := config.ParseFullName(rest[1])
	if err != nil {
		return err
	}
	if err := cfg.ValidateDatabase(); err != nil {
		return err
	}

	dbHandle, err := app.OpenDatabaseHandle(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = dbHandle.Close() }()

	client := app.NewGitHubClient(cfg)
	service := githubsync.NewService(dbHandle.DB, client, app.NewGitIndexService(dbHandle.DB, client, cfg))
	_, err = service.ConfigureRepoBackfill(context.Background(), owner, repo, *mode, *priority)
	return err
}

func runRepair(cfg config.Config, args []string) error {
	repairFlags := flag.NewFlagSet("repair", flag.ContinueOnError)
	if err := repairFlags.Parse(args); err != nil {
		return err
	}

	rest := repairFlags.Args()
	if len(rest) != 3 || rest[0] != "recent" || rest[1] != "repo" {
		return errors.New("usage: ghreplica repair recent repo <owner>/<repo>")
	}
	if err := cfg.ValidateDatabase(); err != nil {
		return err
	}

	owner, repo, err := config.ParseFullName(rest[2])
	if err != nil {
		return err
	}

	dbHandle, err := app.OpenDatabaseHandle(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = dbHandle.Close() }()

	client := app.NewGitHubClient(cfg)
	service := githubsync.NewService(dbHandle.DB, client, app.NewGitIndexService(dbHandle.DB, client, cfg))
	_, err = service.RequestRecentPRRepair(context.Background(), owner, repo)
	return err
}

func runCleanup(cfg config.Config, args []string) error {
	cleanupFlags := flag.NewFlagSet("cleanup", flag.ContinueOnError)
	untilEmpty := cleanupFlags.Bool("until-empty", false, "repeat cleanup passes until no more eligible webhook deliveries remain")
	if err := cleanupFlags.Parse(args); err != nil {
		return err
	}

	rest := cleanupFlags.Args()
	if len(rest) != 1 || rest[0] != "webhook-deliveries" {
		return errors.New("usage: ghreplica cleanup webhook-deliveries [--until-empty]")
	}
	if err := cfg.ValidateDatabase(); err != nil {
		return err
	}
	if cfg.WebhookDeliveryRetention <= 0 {
		return errors.New("WEBHOOK_DELIVERY_RETENTION must be set to use webhook delivery cleanup")
	}

	dbHandle, err := app.OpenDatabaseHandle(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = dbHandle.Close() }()

	worker := webhooks.NewDeliveryCleanupWorker(
		dbHandle.DB,
		cfg.WebhookDeliveryRetention,
		cfg.WebhookDeliveryCleanupInterval,
		cfg.WebhookDeliveryCleanupBatchSize,
	)

	passes := 0
	for {
		processed, err := worker.RunOnce(context.Background())
		if err != nil {
			return err
		}
		passes++
		if !processed || !*untilEmpty {
			fmt.Fprintf(os.Stdout, "cleanup webhook-deliveries passes=%d processed=%t\n", passes, processed)
			return nil
		}
	}
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

	dbHandle, err := app.OpenDatabaseHandle(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = dbHandle.Close() }()

	return searchindex.NewService(dbHandle.DB).RebuildRepository(context.Background(), owner, repo)
}

func usageError() error {
	fmt.Fprintf(os.Stderr, "usage:\n")
	fmt.Fprintf(os.Stderr, "  ghreplica serve\n")
	fmt.Fprintf(os.Stderr, "  ghreplica migrate up\n")
	fmt.Fprintf(os.Stderr, "  ghreplica backfill repo <owner>/<repo> [--mode open_only|open_and_recent|full_history] [--priority N]\n")
	fmt.Fprintf(os.Stderr, "  ghreplica cleanup webhook-deliveries [--until-empty]\n")
	fmt.Fprintf(os.Stderr, "  ghreplica repair recent repo <owner>/<repo>\n")
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
