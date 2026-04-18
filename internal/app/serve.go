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
	if err := cfg.ValidateDatabase(); err != nil {
		return err
	}
	if err := cfg.ValidateServeRuntime(); err != nil {
		return err
	}
	if database.IsSQLiteURL(cfg.DatabaseURL) {
		return errors.New("ghreplica serve requires PostgreSQL when background webhook jobs are enabled")
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
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := runtime.WebhookJobClient.Stop(shutdownCtx); err != nil {
			slog.Error("webhook job client stopped with error", "error", err)
		}
	}()

	go func() {
		if err := runtime.RefreshWorker.Start(ctx); err != nil && ctx.Err() == nil {
			slog.Error("refresh worker stopped", "error", err)
			stop()
		}
	}()
	go func() {
		if err := runtime.ChangeSyncWorker.Start(ctx); err != nil && ctx.Err() == nil {
			slog.Error("change sync worker stopped", "error", err)
			stop()
		}
	}()

	return runtime.Server.Start(ctx, cfg.AppAddr)
}
