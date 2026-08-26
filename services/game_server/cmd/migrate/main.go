package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"texas/services/game_server/internal/config"
	"texas/services/game_server/internal/postgres"
	"texas/services/game_server/migrations"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	appConfig, err := config.Load()
	if err != nil {
		logger.Error("invalid migration configuration", "error", err)
		os.Exit(1)
	}
	if !appConfig.DatabaseConfigured() {
		logger.Error("DATABASE_URL is required for migrations")
		os.Exit(1)
	}
	command := "up"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	flags := flag.NewFlagSet(command, flag.ExitOnError)
	steps := flags.Int("steps", 1, "number of applied migrations to roll back")
	if err := flags.Parse(os.Args[2:]); err != nil {
		logger.Error("invalid migration arguments", "error", err)
		os.Exit(1)
	}

	shutdownContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	connectContext, cancel := context.WithTimeout(shutdownContext, 10*time.Second)
	database, err := postgres.Open(connectContext, appConfig.DatabaseURL)
	cancel()
	if err != nil {
		logger.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	defer database.Close()
	migrator, err := postgres.NewMigrator(migrations.Files)
	if err != nil {
		logger.Error("migration catalog is invalid", "error", err)
		os.Exit(1)
	}

	var applied int
	switch command {
	case "up":
		applied, err = migrator.Up(shutdownContext, database)
	case "down":
		applied, err = migrator.Down(shutdownContext, database, *steps)
	default:
		err = fmt.Errorf("unknown migration command %q; use up or down", command)
	}
	if err != nil {
		logger.Error("migration failed", "command", command, "error", err)
		os.Exit(1)
	}
	logger.Info("migration completed", "command", command, "count", applied)
}
