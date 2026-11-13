// Command lobby-lambda serves the lobby from AWS Lambda behind a Function URL.
// The Function URL must use the RESPONSE_STREAM invoke mode, which lambdaurl
// requires, as the Island Battles backend's Lambda does. Build it for the
// provided.al2023 runtime on arm64:
//
//	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -tags lambda.norpc -trimpath -ldflags='-s -w' -o bootstrap ./cmd/lobby-lambda
//
// The runtime looks for an executable named bootstrap at the root of the zip.
//
// It reads the same LOBBY_* environment as the local server, cmd/lobby,
// except LOBBY_HTTP_ADDR, with the pool limits' Lambda defaults, and runs no
// schema changes: a deploy migrates the lobby's schema before a new build
// serves. It logs JSON lines to standard output.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"islandBattles/lobby/app"

	"github.com/aws/aws-lambda-go/lambdaurl"
)

// startRetryInterval spaces out attempts to start after a failed one.
const startRetryInterval = 10 * time.Second

// deadlineMargin ends a request's work before Lambda's timeout does. A
// timeout resets the instance and logs a platform line.
const deadlineMargin = 2 * time.Second

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	instance := newHost(logger, func(context.Context) (http.Handler, error) { return build(logger) }, time.Now)
	// The first attempt runs during the cold start.
	instance.started(context.Background())
	lambdaurl.Start(instance)
}

// build runs once per cold start. The pool stays open for the instance's
// lifetime, so warm invocations reuse its connections. A failed attempt
// closes its pool, so the retries don't pile up connections.
func build(logger *slog.Logger) (_ http.Handler, err error) {
	config, err := app.LoadConfig()
	if err != nil {
		return nil, err
	}
	database, err := app.OpenDatabase(config.Database, logger)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err == nil {
			return
		}
		if pool, poolError := database.DB(); poolError == nil {
			_ = pool.Close()
		}
	}()
	lobby, err := app.New(config, database, app.NewCentrifugoClient(config), logger, app.WithObserver(app.Observer{
		Error: func(ctx context.Context, err error) { logger.ErrorContext(ctx, "Lobby request failed", "error", err) },
	}))
	if err != nil {
		return nil, err
	}
	return lobby.Handler()
}

// host serves the lobby once it has started. Until then it answers 503 and
// tries to start again at most every startRetryInterval. Failing the cold
// start instead would make Lambda retry it, with a platform log line, on
// every request.
type host struct {
	logger  *slog.Logger
	start   func(context.Context) (http.Handler, error)
	now     func() time.Time
	mutex   sync.Mutex
	handler http.Handler
	tried   time.Time
}

func newHost(logger *slog.Logger, start func(context.Context) (http.Handler, error), now func() time.Time) *host {
	return &host{logger: logger, start: start, now: now}
}

func (instance *host) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	handler := instance.started(request.Context())
	if handler == nil {
		http.Error(response, "the lobby failed to start", http.StatusServiceUnavailable)
		return
	}
	if deadline, ok := request.Context().Deadline(); ok {
		ctx, cancel := context.WithDeadline(request.Context(), deadline.Add(-deadlineMargin))
		defer cancel()
		request = request.WithContext(ctx)
	}
	handler.ServeHTTP(response, request)
}

// started returns the lobby's handler, starting it first if it is time to
// try again, or nil.
func (instance *host) started(ctx context.Context) http.Handler {
	instance.mutex.Lock()
	defer instance.mutex.Unlock()
	if instance.handler != nil {
		return instance.handler
	}
	now := instance.now()
	if !instance.tried.IsZero() && now.Sub(instance.tried) < startRetryInterval {
		return nil
	}
	instance.tried = now
	handler, err := instance.start(ctx)
	if err != nil {
		instance.logger.ErrorContext(ctx, "Lobby failed to start; it answers 503 until it does", "error", err, "retry_after", startRetryInterval.String())
		return nil
	}
	instance.handler = handler
	return handler
}
