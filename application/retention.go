package application

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"

	"islandBattles/lobby/domain"
)

// retentionPassTimeout bounds one pass, so a slow database delays the request
// that runs it by at most this much.
const retentionPassTimeout = 5 * time.Second

// Retention deletes lobby data past its retention period at the end of
// requests, since nothing runs between them. A process runs at most one pass
// at a time, and at most one per PassInterval while passes find no backlog.
// When a pass fills a batch, the next request runs another, so a backlog
// drains at the pace of requests without one request doing all of it.
type Retention struct {
	store  domain.RetentionStore
	clock  domain.Clock
	policy domain.Policy
	logger *slog.Logger

	// running is held for a whole pass; requests that find it held skip.
	running sync.Mutex
	// nextPass is guarded by running.
	nextPass time.Time
}

var _ domain.RetentionRunner = (*Retention)(nil)

func NewRetention(store domain.RetentionStore, clock domain.Clock, policy domain.Policy, logger *slog.Logger) (*Retention, error) {
	if store == nil || clock == nil || logger == nil {
		return nil, domain.ErrInvalidInput
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	policy.Games = slices.Clone(policy.Games)
	return &Retention{store: store, clock: clock, policy: policy, logger: logger}, nil
}

// RunIfDue runs a pass when one is due and none is running in this process.
// It outlives a canceled request, within its own timeout, and logs failures
// instead of returning them, because the request itself has succeeded.
func (retention *Retention) RunIfDue(ctx context.Context) {
	if !retention.running.TryLock() {
		return
	}
	defer retention.running.Unlock()
	now := retention.clock.Now().UTC()
	if now.Before(retention.nextPass) {
		return
	}
	retention.nextPass = now.Add(retention.policy.Retention.PassInterval)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), retentionPassTimeout)
	defer cancel()
	result, err := retention.store.DeleteExpired(ctx, retention.policy.RetentionCutoffs(now))
	if err != nil {
		// The steps that succeeded still count. A failing step waits for
		// the interval, so it isn't retried on every request.
		retention.logger.ErrorContext(ctx, "Lobby retention pass failed", "error", err)
	} else if result.Backlog(retention.policy.Retention.BatchSize) {
		retention.nextPass = now
	}
	if result != (domain.RetentionResult{}) {
		retention.logger.InfoContext(ctx, "Lobby retention pass deleted expired data",
			"create_requests", result.CreateRequests, "sessions", result.Sessions,
			"lobbies", result.Lobbies, "players", result.Players)
	}
}
