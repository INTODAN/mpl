// Package delivery retries committed lobby invalidations and subscription
// revocations. Network calls never run inside a database transaction.
package delivery

import (
	"context"
	"errors"
	"sync"
	"time"

	"islandBattles/lobby/domain"
)

type Config struct {
	BatchSize      int
	ClaimDuration  time.Duration
	NetworkTimeout time.Duration
	PollInterval   time.Duration
	RetryMinimum   time.Duration
	RetryMaximum   time.Duration
}

func DefaultConfig() Config {
	return Config{BatchSize: 16, ClaimDuration: 30 * time.Second, NetworkTimeout: 3 * time.Second, PollInterval: time.Second, RetryMinimum: time.Second, RetryMaximum: time.Minute}
}

type Worker struct {
	store     domain.DeliveryStore
	transport domain.RealtimeTransport
	clock     domain.Clock
	config    Config
}

func NewWorker(store domain.DeliveryStore, transport domain.RealtimeTransport, clock domain.Clock, config Config) (*Worker, error) {
	if store == nil || transport == nil || clock == nil || config.BatchSize < 1 || config.BatchSize > 64 || config.NetworkTimeout <= 0 || config.ClaimDuration <= config.NetworkTimeout || config.PollInterval <= 0 || config.RetryMinimum <= 0 || config.RetryMaximum < config.RetryMinimum {
		return nil, domain.ErrInvalidInput
	}
	return &Worker{store: store, transport: transport, clock: clock, config: config}, nil
}

// RunOnce starts at most BatchSize attempts. Every claim begins promptly, so a
// slow earlier publication cannot consume a later job's entire claim lifetime.
func (worker *Worker) RunOnce(ctx context.Context) error {
	jobs, err := worker.store.Claim(ctx, domain.ClaimRequest{Now: worker.clock.Now().UTC(), Limit: worker.config.BatchSize, ClaimDuration: worker.config.ClaimDuration})
	if err != nil {
		return err
	}
	results := make(chan error, len(jobs))
	var pending sync.WaitGroup
	for _, job := range jobs {
		pending.Add(1)
		go func(job domain.DeliveryJob) { defer pending.Done(); results <- worker.attempt(ctx, job) }(job)
	}
	pending.Wait()
	close(results)
	var failures []error
	for err := range results {
		if err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (worker *Worker) attempt(ctx context.Context, job domain.DeliveryJob) error {
	networkContext, cancel := context.WithTimeout(ctx, worker.config.NetworkTimeout)
	defer cancel()
	var attemptError error
	switch job.Kind {
	case domain.JobInvalidation:
		if job.Invalidation == nil || job.Revocation != nil {
			attemptError = domain.ErrInvalidInput
			break
		}
		channel, err := domain.RoomChannel(domain.Scope{GameKey: job.Invalidation.GameKey, LobbyID: job.Invalidation.LobbyID})
		if err != nil {
			attemptError = err
			break
		}
		attemptError = worker.transport.PublishInvalidation(networkContext, channel, *job.Invalidation)
	case domain.JobRevocation:
		if job.Revocation == nil || job.Invalidation != nil {
			attemptError = domain.ErrInvalidInput
			break
		}
		state, err := worker.store.CurrentMembership(networkContext, job.Revocation.Scope, job.Revocation.PlayerID)
		if err != nil {
			attemptError = err
			break
		}
		if state != nil && state.Active && state.Generation > job.Revocation.Generation {
			break
		}
		channel, err := domain.RoomChannel(job.Revocation.Scope)
		if err != nil {
			attemptError = err
			break
		}
		attemptError = worker.transport.Unsubscribe(networkContext, job.Revocation.PlayerID, channel)
	default:
		attemptError = domain.ErrInvalidInput
	}
	now := worker.clock.Now().UTC()
	if attemptError == nil {
		_, err := worker.store.Ack(ctx, job.ID, job.ClaimToken, now)
		return err
	}
	// A fixed summary intentionally excludes URLs, credentials, raw SQL, and
	// publisher error bodies. Detailed failures remain available to the caller.
	_, retryError := worker.store.Retry(ctx, domain.RetryRequest{JobID: job.ID, ClaimToken: job.ClaimToken, Now: now, NextAttemptAt: now.Add(worker.backoff(job.Attempts)), ErrorSummary: "lobby delivery attempt failed"})
	return errors.Join(attemptError, retryError)
}

func (worker *Worker) backoff(attempts int) time.Duration {
	delay := worker.config.RetryMinimum
	for attempt := 1; attempt < attempts && delay < worker.config.RetryMaximum; attempt++ {
		if delay > worker.config.RetryMaximum/2 {
			return worker.config.RetryMaximum
		}
		delay *= 2
	}
	return min(delay, worker.config.RetryMaximum)
}

func (worker *Worker) Run(ctx context.Context, onError func(error)) {
	ticker := time.NewTicker(worker.config.PollInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if err := worker.RunOnce(ctx); err != nil && onError != nil && ctx.Err() == nil {
			onError(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
