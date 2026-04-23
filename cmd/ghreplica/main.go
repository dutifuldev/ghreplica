package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/dutifuldev/ghreplica/internal/app"
	"github.com/dutifuldev/ghreplica/internal/config"
	"github.com/dutifuldev/ghreplica/internal/githubsync"
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
	return runCommand(cfg, args[0], args[1:])
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
	request, err := parseSyncRequest(syncFlags.Args())
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
	db := dbHandle.DB

	client := app.NewGitHubClient(cfg)
	service := githubsync.NewService(db, client, app.NewGitIndexService(db, client, cfg)).
		WithOpenPRInventoryMaxAge(cfg.OpenPRInventoryMaxAge)
	return executeSyncRequest(context.Background(), service, request)
}

func runRefresh(cfg config.Config, args []string) error {
	refreshFlags := flag.NewFlagSet("refresh", flag.ContinueOnError)
	if err := refreshFlags.Parse(args); err != nil {
		return err
	}

	rest := refreshFlags.Args()
	if len(rest) == 3 && rest[0] == "inventory" && rest[1] == "repo" {
		return runRefreshInventory(cfg, rest[2])
	}
	if len(rest) != 2 || rest[0] != "repo" {
		return errors.New("usage: ghreplica refresh {repo <owner>/<repo> | inventory repo <owner>/<repo>}")
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

func runRefreshInventory(cfg config.Config, fullName string) error {
	owner, repo, err := config.ParseFullName(fullName)
	if err != nil {
		return err
	}
	if err := cfg.ValidateDatabase(); err != nil {
		return err
	}

	dbHandle, err := app.OpenSyncDatabaseHandle(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = dbHandle.Close() }()

	client := app.NewGitHubClient(cfg)
	service := githubsync.NewService(dbHandle.DB, client, app.NewGitIndexService(dbHandle.DB, client, cfg)).
		WithOpenPRInventoryMaxAge(cfg.OpenPRInventoryMaxAge)
	result, err := service.RefreshOpenPullInventoryNow(context.Background(), owner, repo, cfg.RepoLeaseTTL)
	if err != nil {
		return err
	}

	status, err := service.GetRepoChangeStatus(context.Background(), owner, repo)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(
		os.Stdout,
		"refresh inventory repo=%s total=%d current=%d stale=%d missing=%d generation=%d committed_at=%s\n",
		fullName,
		result.OpenPRTotal,
		result.OpenPRCurrent,
		result.OpenPRStale,
		result.OpenPRMissing,
		status.InventoryGenerationCurrent,
		formatTimePtr(status.InventoryLastCommittedAt),
	)
	return err
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
	service := githubsync.NewService(dbHandle.DB, client, app.NewGitIndexService(dbHandle.DB, client, cfg)).
		WithOpenPRInventoryMaxAge(cfg.OpenPRInventoryMaxAge)
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
	service := githubsync.NewService(dbHandle.DB, client, app.NewGitIndexService(dbHandle.DB, client, cfg)).
		WithOpenPRInventoryMaxAge(cfg.OpenPRInventoryMaxAge)
	_, err = service.RequestRecentPRRepair(context.Background(), owner, repo)
	return err
}

func runCleanup(cfg config.Config, args []string) error {
	cleanupFlags := flag.NewFlagSet("cleanup", flag.ContinueOnError)
	untilEmpty := cleanupFlags.Bool("until-empty", false, "repeat cleanup passes until no more eligible webhook deliveries remain")
	if err := cleanupFlags.Parse(trimCleanupTarget(args)); err != nil {
		return err
	}
	if !validCleanupArgs(args, cleanupFlags.Args()) {
		return errors.New("usage: ghreplica cleanup webhook-deliveries [--until-empty]")
	}
	if err := cfg.ValidateDatabase(); err != nil {
		return err
	}
	if cfg.WebhookDeliveryRetention <= 0 {
		return errors.New("WEBHOOK_DELIVERY_RETENTION must be set to use webhook delivery cleanup")
	}

	dbHandle, err := app.OpenWebhookDatabaseHandle(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = dbHandle.Close() }()

	passes, processed, err := runDeliveryCleanup(context.Background(), newDeliveryCleanupWorker(dbHandle.DB, cfg), *untilEmpty)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(os.Stdout, "cleanup webhook-deliveries passes=%d processed=%t\n", passes, processed)
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

	dbHandle, err := app.OpenDatabaseHandle(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = dbHandle.Close() }()

	return searchindex.NewService(dbHandle.DB).RebuildRepository(context.Background(), owner, repo)
}

func usageError() error {
	for _, line := range []string{
		"usage:\n",
		"  ghreplica serve\n",
		"  ghreplica migrate up\n",
		"  ghreplica backfill repo <owner>/<repo> [--mode open_only|open_and_recent|full_history] [--priority N]\n",
		"  ghreplica cleanup webhook-deliveries [--until-empty]\n",
		"  ghreplica repair recent repo <owner>/<repo>\n",
		"  ghreplica refresh repo <owner>/<repo>\n",
		"  ghreplica refresh inventory repo <owner>/<repo>\n",
		"  ghreplica search-index repo <owner>/<repo>\n",
		"  ghreplica sync repo <owner>/<repo>\n",
		"  ghreplica sync issue <owner>/<repo> <number>\n",
		"  ghreplica sync pr <owner>/<repo> <number>\n",
	} {
		if _, err := io.WriteString(os.Stderr, line); err != nil {
			return err
		}
	}
	return errors.New("invalid command")
}

func parseNumberArg(raw string) (int, error) {
	number, err := strconv.Atoi(raw)
	if err != nil || number <= 0 {
		return 0, fmt.Errorf("invalid number: %q", raw)
	}
	return number, nil
}

func formatTimePtr(at *time.Time) string {
	if at == nil {
		return ""
	}
	return at.UTC().Format(time.RFC3339)
}

type syncRequest struct {
	target   string
	fullName string
	number   int
}

func runCommand(cfg config.Config, name string, args []string) error {
	handlers := map[string]func(config.Config, []string) error{
		"serve":        func(cfg config.Config, _ []string) error { return runServe(cfg) },
		"migrate":      runMigrate,
		"backfill":     runBackfill,
		"refresh":      runRefresh,
		"repair":       runRepair,
		"cleanup":      runCleanup,
		"sync":         runSync,
		"search-index": runSearchIndex,
	}
	handler, ok := handlers[name]
	if !ok {
		return usageError()
	}
	return handler(cfg, args)
}

func parseSyncRequest(args []string) (syncRequest, error) {
	if len(args) < 2 {
		return syncRequest{}, syncUsageError()
	}

	switch args[0] {
	case "repo":
		if len(args) != 2 {
			return syncRequest{}, errors.New("usage: ghreplica sync repo <owner>/<repo>")
		}
		return syncRequest{target: "repo", fullName: args[1]}, nil
	case "issue", "pr":
		if len(args) != 3 {
			return syncRequest{}, fmt.Errorf("usage: ghreplica sync %s <owner>/<repo> <number>", args[0])
		}
		number, err := parseNumberArg(args[2])
		if err != nil {
			return syncRequest{}, err
		}
		return syncRequest{target: args[0], fullName: args[1], number: number}, nil
	default:
		return syncRequest{}, syncUsageError()
	}
}

func executeSyncRequest(ctx context.Context, service *githubsync.Service, request syncRequest) error {
	owner, repo, err := config.ParseFullName(request.fullName)
	if err != nil {
		return err
	}

	switch request.target {
	case "repo":
		return service.BootstrapRepository(ctx, owner, repo)
	case "issue":
		return service.SyncIssue(ctx, owner, repo, request.number)
	case "pr":
		return service.SyncPullRequest(ctx, owner, repo, request.number)
	default:
		return syncUsageError()
	}
}

func syncUsageError() error {
	return errors.New("usage: ghreplica sync {repo <owner>/<repo> | issue <owner>/<repo> <number> | pr <owner>/<repo> <number>}")
}

func trimCleanupTarget(args []string) []string {
	if len(args) >= 1 && args[0] == "webhook-deliveries" {
		return args[1:]
	}
	return args
}

func validCleanupArgs(args, parsedArgs []string) bool {
	rest := append([]string(nil), parsedArgs...)
	if len(args) >= 1 && args[0] == "webhook-deliveries" {
		rest = append([]string{"webhook-deliveries"}, rest...)
	}
	return len(rest) == 1 && rest[0] == "webhook-deliveries"
}

func newDeliveryCleanupWorker(db *gorm.DB, cfg config.Config) *webhooks.DeliveryCleanupWorker {
	return webhooks.NewDeliveryCleanupWorker(
		db,
		cfg.WebhookDeliveryRetention,
		cfg.WebhookDeliveryCleanupInterval,
		cfg.WebhookDeliveryCleanupBatchSize,
	)
}

func runDeliveryCleanup(ctx context.Context, worker *webhooks.DeliveryCleanupWorker, untilEmpty bool) (int, bool, error) {
	passes := 0
	for {
		processed, err := worker.RunOnce(ctx)
		if err != nil {
			return 0, false, err
		}
		passes++
		if !processed || !untilEmpty {
			return passes, processed, nil
		}
	}
}
