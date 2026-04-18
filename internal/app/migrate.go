package app

import (
	"context"
	"errors"

	"github.com/dutifuldev/ghreplica/internal/config"
	"github.com/dutifuldev/ghreplica/internal/database"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivermigrate"
)

func RunMigrate(cfg config.Config, args []string) error {
	if err := cfg.ValidateDatabase(); err != nil {
		return err
	}
	if len(args) != 1 || args[0] != "up" {
		return errors.New("usage: ghreplica migrate up")
	}

	db, err := OpenDatabase(cfg)
	if err != nil {
		return err
	}

	if database.IsSQLiteURL(cfg.DatabaseURL) {
		return database.AutoMigrate(db)
	}
	if err := database.RunMigrations(db, "migrations"); err != nil {
		return err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	migrator, err := rivermigrate.New(riverdatabasesql.New(sqlDB), nil)
	if err != nil {
		return err
	}
	_, err = migrator.Migrate(context.Background(), rivermigrate.DirectionUp, nil)
	return err
}
