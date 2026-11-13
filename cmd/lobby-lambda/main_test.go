package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A lobby that can't start answers 503 and tries again at most every
// startRetryInterval, rather than failing the cold start, which Lambda would
// retry with a platform log line on every request.
func TestTheLambdaHostAnswers503UntilTheLobbyStarts(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	attempts := 0
	instance := newHost(logger, func(context.Context) (http.Handler, error) {
		attempts++
		if attempts == 1 {
			return nil, errors.New("database unreachable")
		}
		return http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { response.WriteHeader(http.StatusNoContent) }), nil
	}, func() time.Time { return now })
	serve := func() int {
		recorder := httptest.NewRecorder()
		instance.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		return recorder.Code
	}
	if status := serve(); status != http.StatusServiceUnavailable || attempts != 1 {
		t.Fatalf("first request=%d after %d attempts", status, attempts)
	}
	if status := serve(); status != http.StatusServiceUnavailable || attempts != 1 {
		t.Fatalf("request within the retry interval=%d after %d attempts", status, attempts)
	}
	if lines := strings.Count(output.String(), "\n"); lines != 1 || !strings.Contains(output.String(), "database unreachable") {
		t.Fatalf("logged %d lines: %s", lines, output.String())
	}
	now = now.Add(startRetryInterval)
	if status := serve(); status != http.StatusNoContent || attempts != 2 {
		t.Fatalf("request after the retry interval=%d after %d attempts", status, attempts)
	}
}

// A request's work ends before Lambda's timeout, which would reset the
// instance and log a platform line.
func TestRequestsEndBeforeTheLambdaTimeout(t *testing.T) {
	var deadline time.Time
	instance := newHost(slog.New(slog.NewJSONHandler(io.Discard, nil)), func(context.Context) (http.Handler, error) {
		return http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) { deadline, _ = request.Context().Deadline() }), nil
	}, time.Now)
	timeout := time.Now().Add(30 * time.Second)
	ctx, cancel := context.WithDeadline(context.Background(), timeout)
	defer cancel()
	instance.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil).WithContext(ctx))
	if !deadline.Equal(timeout.Add(-deadlineMargin)) {
		t.Fatalf("request deadline = %v, want %v before the invocation's %v", deadline, deadlineMargin, timeout)
	}
}

// Without its settings the lobby doesn't start, and says which are missing.
func TestBuildNamesTheMissingSettings(t *testing.T) {
	for _, name := range []string{"LOBBY_DATABASE_URL", "LOBBY_PUBLIC_URL", "LOBBY_GAMES"} {
		t.Setenv(name, "")
	}
	_, err := build(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err == nil || !strings.Contains(err.Error(), "LOBBY_DATABASE_URL") || !strings.Contains(err.Error(), "LOBBY_GAMES") {
		t.Fatalf("build() error = %v", err)
	}
}
