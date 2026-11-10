package postgres_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"islandBattles/lobby/domain"
	"islandBattles/lobby/postgres"
)

// The store keeps a started lobby's roster frozen. Only reopening the lobby,
// back to waiting in its next round, empties it, and the next start writes a
// new one.
func TestStoreEmptiesTheRosterOnlyWhenTheLobbyReopens(t *testing.T) {
	fixture := newStoreFixture(t)
	scope := fixture.aggregate.Lobby.Scope()
	transact := func(mutation func(*domain.Aggregate)) error {
		_, err := fixture.store.Transact(context.Background(), scope, func(aggregate *domain.Aggregate) (domain.Change, error) {
			mutation(aggregate)
			return domain.Change{PublicChanged: true}, nil
		})
		return err
	}
	start := func(aggregate *domain.Aggregate) {
		member := &aggregate.Members[0]
		member.TeamID = &aggregate.Teams[0].ID
		aggregate.Lobby.Phase = domain.PhaseStarted
		aggregate.StartRoster = domain.StartRoster{{PlayerID: member.PlayerID, TeamID: aggregate.Teams[0].ID, MemberID: member.ID, TeamOrder: 1}}
	}
	refused := func(name string, mutation func(*domain.Aggregate)) {
		t.Helper()
		before := readStore(t, fixture)
		if err := transact(mutation); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("%s: err = %v, want invalid_input", name, err)
		}
		if !reflect.DeepEqual(before, readStore(t, fixture)) {
			t.Fatalf("%s changed the lobby", name)
		}
	}

	refused("a round changed while the lobby waits", func(aggregate *domain.Aggregate) { aggregate.Lobby.Round++ })
	if err := transact(start); err != nil {
		t.Fatal(err)
	}
	if rows := storeCount(t, fixture.db, &postgres.StartRosterRecord{}); rows != 1 {
		t.Fatalf("roster rows = %d, want 1", rows)
	}
	refused("a roster emptied while started", func(aggregate *domain.Aggregate) { aggregate.StartRoster = domain.StartRoster{} })
	refused("a round changed while started", func(aggregate *domain.Aggregate) { aggregate.Lobby.Round++ })
	refused("a reopen in the same round", func(aggregate *domain.Aggregate) {
		aggregate.Lobby.Phase = domain.PhaseWaiting
		aggregate.StartRoster = domain.StartRoster{}
	})
	refused("a reopen that skips a round", func(aggregate *domain.Aggregate) {
		aggregate.Lobby.Phase = domain.PhaseWaiting
		aggregate.Lobby.Round += 2
		aggregate.StartRoster = domain.StartRoster{}
	})
	refused("a reopen that keeps the roster", func(aggregate *domain.Aggregate) {
		aggregate.Lobby.Phase = domain.PhaseWaiting
		aggregate.Lobby.Round++
	})

	if err := transact(func(aggregate *domain.Aggregate) {
		aggregate.Lobby.Phase = domain.PhaseWaiting
		aggregate.Lobby.Round++
		aggregate.StartRoster = domain.StartRoster{}
	}); err != nil {
		t.Fatal(err)
	}
	reopened := readStore(t, fixture)
	if reopened.Lobby.Phase != domain.PhaseWaiting || reopened.Lobby.Round != 2 || len(reopened.StartRoster) != 0 {
		t.Fatalf("reopened lobby = phase %s, round %d, roster %v", reopened.Lobby.Phase, reopened.Lobby.Round, reopened.StartRoster)
	}
	if rows := storeCount(t, fixture.db, &postgres.StartRosterRecord{}); rows != 0 {
		t.Fatalf("roster rows after reopening = %d, want 0", rows)
	}

	if err := transact(start); err != nil {
		t.Fatalf("second start: %v", err)
	}
	restarted := readStore(t, fixture)
	if restarted.Lobby.Phase != domain.PhaseStarted || restarted.Lobby.Round != 2 || len(restarted.StartRoster) != 1 {
		t.Fatalf("second start = phase %s, round %d, roster %v", restarted.Lobby.Phase, restarted.Lobby.Round, restarted.StartRoster)
	}
	refused("a second roster changed", func(aggregate *domain.Aggregate) { aggregate.StartRoster[0].TeamOrder = 2 })
}
