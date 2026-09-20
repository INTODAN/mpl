package lobby

import (
	"context"
	"errors"
	"time"
)

// Sweeper processes persistent lease deadlines. It owns no database connection
// and starts no goroutine until Run is called by application bootstrap.
type Sweeper struct {
	store     Store
	clock     Clock
	policy    Policy
	batchSize int
}

func NewSweeper(store Store, clock Clock, policy Policy, batchSize int) (*Sweeper, error) {
	if store == nil || clock == nil || batchSize < 1 || batchSize > 1000 {
		return nil, ErrInvalidInput
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	policy.RegisteredGameKeys = append([]string{}, policy.RegisteredGameKeys...)
	return &Sweeper{store: store, clock: clock, policy: policy, batchSize: batchSize}, nil
}

// RunOnce continues past individual room failures. Store.DueScopes rotates
// bounded candidates so a repeatedly failing room cannot starve the rest.
func (sweeper *Sweeper) RunOnce(ctx context.Context) error {
	scopes, err := sweeper.store.DueScopes(ctx, sweeper.clock.Now().UTC(), sweeper.batchSize)
	if err != nil {
		return err
	}
	var failures []error
	for _, scope := range scopes {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		_, err := sweeper.store.Transact(ctx, scope, func(aggregate *Aggregate) (Change, error) {
			return NormalizeExpired(aggregate, sweeper.clock.Now().UTC(), sweeper.policy)
		})
		if err != nil && !errors.Is(err, ErrNotFound) {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// Run retries failed passes on the next tick; onError may report failures but
// must return promptly. Cancellation always stops the owned timer.
func (sweeper *Sweeper) Run(ctx context.Context, onError func(error)) {
	ticker := time.NewTicker(sweeper.policy.SweepInterval)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		if err := sweeper.RunOnce(ctx); err != nil && onError != nil && ctx.Err() == nil {
			onError(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
