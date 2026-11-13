// Command migrate brings the lobby's database to the schema this build
// expects: the lobby's versioned migrations (postgres.Migrate), which keep
// existing rows, and nothing else. A deploy runs it once, before a new build
// serves; the Lambda host, cmd/lobby-lambda, runs no schema changes. It reads
// LOBBY_DATABASE_URL and the optional pool settings, and nothing else, so a
// deploy script can run it with the database's credentials alone.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"islandBattles/lobby/app"
	lobbypostgres "islandBattles/lobby/postgres"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	config, err := app.LoadDatabaseConfigFromLookup(os.LookupEnv)
	if err != nil {
		logger.Error("Invalid configuration", "error", err)
		os.Exit(1)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, config, logger); err != nil {
		logger.Error("Migration failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, config app.DatabaseConfig, logger *slog.Logger) error {
	database, err := app.OpenDatabase(config, logger)
	if err != nil {
		return err
	}
	pool, err := database.DB()
	if err != nil {
		return err
	}
	defer func() { _ = pool.Close() }()
	if err := lobbypostgres.Migrate(ctx, database); err != nil {
		return err
	}
	logger.Info("The lobby's schema is up to date")
	return nil
}
