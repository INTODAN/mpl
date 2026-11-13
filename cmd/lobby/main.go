// Command lobby serves the lobby on its own with net/http, for local
// development and live tests. It reads the lobby's LOBBY_* environment
// (lobby/README.md, "Running the lobby"), listens on LOBBY_HTTP_ADDR, :8890
// by default, and brings the lobby's schema up to date before it listens,
// which a local server can do safely: migrations run under an advisory lock.
// The AWS Lambda host of the same lobby is cmd/lobby-lambda.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"islandBattles/lobby/app"
	lobbypostgres "islandBattles/lobby/postgres"
)

// LocalMaxOpenConnections is this host's default pool limit, which
// LOBBY_DATABASE_MAX_OPEN_CONNECTIONS overrides. This host serves many
// requests at once. A ready or start request holds a connection, and the
// room's lock, while it calls the game's ready-check hook, and the game may
// read the lobby through the game API meanwhile, which takes a second
// connection; other requests to the same lobby hold theirs while they wait
// for the lock. So the pool is wider than Lambda's, where an instance serves
// one request at a time and the game API's read reaches another instance.
const LocalMaxOpenConnections = 10

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	config, address, err := load(os.LookupEnv)
	if err != nil {
		logger.Error("Invalid configuration", "error", err)
		os.Exit(1)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, config, address, logger); err != nil {
		logger.Error("Lobby stopped", "error", err)
		os.Exit(1)
	}
}

// load reads the lobby's settings and this host's address, with this host's
// wider pool unless the environment sets one.
func load(lookup func(string) (string, bool)) (app.Config, string, error) {
	config, err := app.LoadConfigFromLookup(lookup)
	if err != nil {
		return app.Config{}, "", err
	}
	address, err := app.LoadHTTPAddr(lookup)
	if err != nil {
		return app.Config{}, "", err
	}
	if _, set := lookup(app.DatabaseMaxOpenConnectionsEnv); !set {
		config.Database.MaxOpenConnections = LocalMaxOpenConnections
	}
	return config, address, nil
}

func run(ctx context.Context, config app.Config, address string, logger *slog.Logger) error {
	database, err := app.OpenDatabase(config.Database, logger)
	if err != nil {
		return err
	}
	pool, err := database.DB()
	if err != nil {
		return err
	}
	defer func() { _ = pool.Close() }()
	if err := lobbypostgres.Migrate(ctx, database); err != nil {
		return fmt.Errorf("migrate the lobby's schema: %w", err)
	}
	lobby, err := app.New(config, database, app.NewCentrifugoClient(config), logger, app.WithObserver(app.Observer{
		Error: func(ctx context.Context, err error) { logger.ErrorContext(ctx, "Lobby request failed", "error", err) },
	}))
	if err != nil {
		return err
	}
	handler, err := lobby.Handler()
	if err != nil {
		return err
	}
	server := &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	shutdownDone := make(chan struct{})
	serverContext, stopServer := context.WithCancel(ctx)
	defer stopServer()
	go func() {
		defer close(shutdownDone)
		<-serverContext.Done()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			logger.Error("HTTP shutdown timed out", "error", err)
			_ = server.Close()
		}
	}()
	logger.Info("Lobby listening", "address", address, "public_url", config.PublicURL)
	serveError := server.ListenAndServe()
	stopServer()
	<-shutdownDone
	if serveError != nil && !errors.Is(serveError, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTP: %w", serveError)
	}
	return nil
}
