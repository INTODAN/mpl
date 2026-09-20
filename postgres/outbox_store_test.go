package postgres_test

import (
	"context"
	"testing"
	"time"

	"islandBattles/lobby"
	"islandBattles/lobby/postgres"
)

func claimStoreJobs(t *testing.T, fixture *storeFixture, now time.Time, limit int) []lobby.DeliveryJob {
	t.Helper()
	jobs, err := fixture.store.Claim(context.Background(), lobby.ClaimRequest{Now: now, Limit: limit, ClaimDuration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	return jobs
}
func TestDeliveryClaimsAreExclusive(t *testing.T) {
	fixture := newStoreFixture(t)
	_, err := fixture.store.Transact(context.Background(), fixture.aggregate.Lobby.Scope(), func(aggregate *lobby.Aggregate) (lobby.Change, error) {
		aggregate.Lobby.Name = "Changed"
		return lobby.Change{PublicChanged: true}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := postgres.NewStore(fixture.db, fixture.clock, lobby.CryptoIdentifiers{})
	if err != nil {
		t.Fatal(err)
	}
	barrier := make(chan struct{})
	results := make(chan []lobby.DeliveryJob, 2)
	failures := make(chan error, 2)
	for _, store := range []*postgres.Store{fixture.store, other} {
		go func() {
			<-barrier
			jobs, err := store.Claim(context.Background(), lobby.ClaimRequest{Now: fixture.clock.now, Limit: 1, ClaimDuration: time.Minute})
			results <- jobs
			failures <- err
		}()
	}
	close(barrier)
	seen := map[string]bool{}
	for range 2 {
		jobs := <-results
		if err := <-failures; err != nil {
			t.Fatal(err)
		}
		if len(jobs) != 1 {
			t.Fatal("worker failed to claim independent job", jobs)
		}
		if seen[jobs[0].ID] {
			t.Fatal("same job claimed by both workers")
		}
		seen[jobs[0].ID] = true
	}
	if jobs := claimStoreJobs(t, fixture, fixture.clock.now, 10); len(jobs) != 0 {
		t.Fatal("active claims reclaimed")
	}
}
func TestExpiredClaimCanBeReclaimed(t *testing.T) {
	fixture := newStoreFixture(t)
	first := claimStoreJobs(t, fixture, fixture.clock.now, 1)
	if len(first) != 1 {
		t.Fatal(first)
	}
	second := claimStoreJobs(t, fixture, fixture.clock.now.Add(time.Minute), 1)
	if len(second) != 1 {
		t.Fatal(second)
	}
	if first[0].ID != second[0].ID || first[0].Invalidation.EventID != second[0].Invalidation.EventID || first[0].ClaimToken == second[0].ClaimToken || second[0].Attempts != 2 {
		t.Fatal("reclaim changed event or retained ownership", first, second)
	}
}
func TestStaleClaimCannotAckOrRetry(t *testing.T) {
	fixture := newStoreFixture(t)
	first := claimStoreJobs(t, fixture, fixture.clock.now, 1)[0]
	expired := fixture.clock.now.Add(time.Minute)
	if affected, err := fixture.store.Ack(context.Background(), first.ID, first.ClaimToken, expired); err != nil || affected {
		t.Fatal("expired claim acknowledged", err)
	}
	second := claimStoreJobs(t, fixture, expired, 1)[0]
	if affected, err := fixture.store.Ack(context.Background(), first.ID, first.ClaimToken, expired); err != nil || affected {
		t.Fatal("stale token acknowledged", err)
	}
	if affected, err := fixture.store.Retry(context.Background(), lobby.RetryRequest{JobID: first.ID, ClaimToken: first.ClaimToken, Now: expired, NextAttemptAt: expired.Add(time.Hour), ErrorSummary: "transient"}); err != nil || affected {
		t.Fatal("stale token rescheduled", err)
	}
	if affected, err := fixture.store.Ack(context.Background(), second.ID, second.ClaimToken, expired); err != nil || !affected {
		t.Fatal("new owner lost claim", err)
	}
}
func TestAcknowledgedJobIsNotReclaimed(t *testing.T) {
	fixture := newStoreFixture(t)
	job := claimStoreJobs(t, fixture, fixture.clock.now, 1)[0]
	if affected, err := fixture.store.Ack(context.Background(), job.ID, job.ClaimToken, fixture.clock.now); err != nil || !affected {
		t.Fatal(err)
	}
	if jobs := claimStoreJobs(t, fixture, fixture.clock.now.Add(24*time.Hour), 10); len(jobs) != 0 {
		t.Fatal("acknowledged job redelivered")
	}
	if affected, err := fixture.store.Ack(context.Background(), job.ID, job.ClaimToken, fixture.clock.now); err != nil || affected {
		t.Fatal("second ack retained claim", err)
	}
}
func TestDeliveryRetryPersistsOriginalEventAndBackoff(t *testing.T) {
	fixture := newStoreFixture(t)
	first := claimStoreJobs(t, fixture, fixture.clock.now, 1)[0]
	next := fixture.clock.now.Add(5 * time.Minute)
	if affected, err := fixture.store.Retry(context.Background(), lobby.RetryRequest{JobID: first.ID, ClaimToken: first.ClaimToken, Now: fixture.clock.now, NextAttemptAt: next, ErrorSummary: "transport unavailable"}); err != nil || !affected {
		t.Fatal("retry did not persist", err)
	}
	if jobs := claimStoreJobs(t, fixture, next.Add(-time.Microsecond), 1); len(jobs) != 0 {
		t.Fatal("retry ignored next attempt")
	}
	second := claimStoreJobs(t, fixture, next, 1)[0]
	if second.ID != first.ID || second.Invalidation.EventID != first.Invalidation.EventID || second.Attempts != 2 || second.LastError != "transport unavailable" {
		t.Fatal("retry lost state", second)
	}
}
func TestDeliveryCurrentMembershipHonorsScope(t *testing.T) {
	fixture := newStoreFixture(t)
	state, err := fixture.store.CurrentMembership(context.Background(), fixture.aggregate.Lobby.Scope(), fixture.principal.PlayerID)
	if err != nil || state == nil || state.Generation != 1 || !state.Active {
		t.Fatal(state, err)
	}
	scope := fixture.aggregate.Lobby.Scope()
	scope.GameKey = "other-game"
	state, err = fixture.store.CurrentMembership(context.Background(), scope, fixture.principal.PlayerID)
	if err != nil || state != nil {
		t.Fatal("wrong game disclosed membership", state, err)
	}
}
