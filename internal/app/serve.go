package app

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dutifuldev/ghreplica/internal/config"
	"github.com/dutifuldev/ghreplica/internal/database"
)

func RunServe(cfg config.Config) error {
	if err := validateServeConfig(cfg); err != nil {
		return err
	}

	runtime, err := NewServeRuntime(cfg)
	if err != nil {
		return err
	}
	defer func() {
		if err := runtime.Close(); err != nil {
			slog.Error("serve runtime closed with error", "error", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := runtime.WebhookJobClient.Start(ctx); err != nil {
		return err
	}
	defer stopWebhookJobClient(runtime)
	startServeWorkers(ctx, stop, runtime)

	return runtime.Server.Start(ctx, cfg.AppAddr)
}

func validateServeConfig(cfg config.Config) error {
	if err := cfg.ValidateDatabase(); err != nil {
		return err
	}
	if err := cfg.ValidateServeRuntime(); err != nil {
		return err
	}
	if database.IsSQLiteURL(cfg.DatabaseURL) {
		return errors.New("ghreplica serve requires PostgreSQL when background webhook jobs are enabled")
	}
	return nil
}

func stopWebhookJobClient(runtime *ServeRuntime) {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := runtime.WebhookJobClient.Stop(shutdownCtx); err != nil {
		slog.Error("webhook job client stopped with error", "error", err)
	}
}

func startServeWorkers(ctx context.Context, stop context.CancelFunc, runtime *ServeRuntime) {
	startServeWorker(ctx, stop, "refresh worker", runtime.RefreshWorker.Start, true)
	startServeWorker(ctx, stop, "webhook cleanup worker", runtime.WebhookCleanupWorker.Start, false)
	startServeWorker(ctx, stop, "change sync worker", runtime.ChangeSyncWorker.Start, true)
}

func startServeWorker(ctx context.Context, stop context.CancelFunc, name string, run func(context.Context) error, stopOnFailure bool) {
	go func() {
		if err := run(ctx); err != nil && ctx.Err() == nil {
			slog.Error(name+" stopped", "error", err)
			if stopOnFailure {
				stop()
			}
		}
	}()
}
