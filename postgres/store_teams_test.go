package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"islandBattles/lobby/domain"
	"islandBattles/lobby/postgres"
)

// changeTeams commits a settings change that gives the lobby these team
// names: kept teams by position, new teams after them, and the capacity of
// the new count. edit can move members in the same transaction.
func changeTeams(t *testing.T, fixture *storeFixture, names []string, edit func(*domain.Aggregate, []string)) (domain.Aggregate, error) {
	t.Helper()
	newTeamIDs := []string{}
	for range names {
		newTeamIDs = append(newTeamIDs, storeUUID(t))
	}
	return fixture.store.Transact(context.Background(), fixture.aggregate.Lobby.Scope(), func(aggregate *domain.Aggregate) (domain.Change, error) {
		rebuilt := make([]domain.Team, len(names))
		for index, name := range names {
			if index < len(aggregate.Teams) {
				rebuilt[index] = aggregate.Teams[index]
				rebuilt[index].Name = name
				continue
			}
			rebuilt[index] = domain.Team{ID: newTeamIDs[index], LobbyID: aggregate.Lobby.ID, Name: name, Order: rebuilt[index-1].Order + 1}
		}
		removed := []string{}
		for _, team := range aggregate.Teams[min(len(names), len(aggregate.Teams)):] {
			removed = append(removed, team.ID)
		}
		aggregate.Teams = rebuilt
		aggregate.Lobby.Capacity = len(rebuilt)
		if edit != nil {
			edit(aggregate, removed)
		}
		return domain.Change{PublicChanged: true}, nil
	})
}

func teamNames(aggregate domain.Aggregate) []string {
	names := []string{}
	for _, team := range aggregate.Teams {
		names = append(names, team.Name)
	}
	return names
}

func TestStoreRebuildsTeamsWhileTheLobbyWaits(t *testing.T) {
	fixture := newStoreFixture(t)
	original := readStore(t, fixture)
	red, blue := original.Teams[0], original.Teams[1]

	// Two teams swap names: neither name is free until the other moves.
	swapped, err := changeTeams(t, fixture, []string{"blue", "Red"}, func(aggregate *domain.Aggregate, _ []string) {
		aggregate.Members[0].TeamID = &aggregate.Teams[0].ID
	})
	if err != nil || !reflect.DeepEqual(teamNames(swapped), []string{"blue", "Red"}) || swapped.Teams[0].ID != red.ID || swapped.Teams[1].ID != blue.ID || swapped.Lobby.Version != 2 {
		t.Fatalf("swapped teams = %#v, %v", swapped.Teams, err)
	}

	// A new team can take a name a kept team gives up, and a member can take
	// the new team in the same change.
	grown, err := changeTeams(t, fixture, []string{"Green", "Red", "Blue"}, func(aggregate *domain.Aggregate, _ []string) {
		aggregate.Members[0].TeamID = &aggregate.Teams[2].ID
	})
	if err != nil || !reflect.DeepEqual(teamNames(grown), []string{"Green", "Red", "Blue"}) || grown.Teams[2].Order != 3 || grown.Lobby.Capacity != 3 {
		t.Fatalf("grown teams = %#v, %v", grown.Teams, err)
	}
	if host := grown.Members[0]; host.TeamID == nil || *host.TeamID != grown.Teams[2].ID {
		t.Fatalf("host = %#v", host)
	}

	// A seat on the third team, then a change that removes that team.
	seatTeam := grown.Teams[2].ID
	if _, err := fixture.store.Transact(context.Background(), fixture.aggregate.Lobby.Scope(), func(aggregate *domain.Aggregate) (domain.Change, error) {
		aggregate.Members[0].TeamID = &aggregate.Teams[1].ID
		aggregate.Members = append(aggregate.Members, domain.Member{ID: storeUUID(t), PlayerID: storeUUID(t), DisplayName: "Bot", Generation: 1, Active: true, TeamID: &seatTeam, Ready: true, JoinedAt: fixture.clock.now, JoinSequence: 2, Kind: domain.MemberSeat, SeatSettings: json.RawMessage(`{"level":1}`)})
		aggregate.Lobby.NextJoinSequence++
		return domain.Change{PublicChanged: true}, nil
	}); err != nil {
		t.Fatal(err)
	}

	// A kept team can take the name of a removed one. The seat on the
	// removed team leaves it in the same change.
	shrunk, err := changeTeams(t, fixture, []string{"Blue", "Gold"}, func(aggregate *domain.Aggregate, removed []string) {
		for index := range aggregate.Members {
			member := &aggregate.Members[index]
			if member.IsSeat() && member.TeamID != nil && *member.TeamID == removed[0] {
				member.Active, member.TeamID, member.Ready = false, nil, false
			}
		}
	})
	if err != nil || !reflect.DeepEqual(teamNames(shrunk), []string{"Blue", "Gold"}) || shrunk.Lobby.Capacity != 2 || shrunk.Teams[0].ID != red.ID || shrunk.Teams[1].ID != blue.ID {
		t.Fatalf("shrunk teams = %#v, %v", shrunk.Teams, err)
	}
	seat := shrunk.Members[1]
	if !seat.IsSeat() || seat.Active || seat.TeamID != nil {
		t.Fatalf("seat on the removed team = %#v", seat)
	}
	if host := shrunk.Members[0]; host.TeamID == nil || *host.TeamID != blue.ID {
		t.Fatalf("host = %#v", host)
	}
	if count := storeCount(t, fixture.db.Where(&postgres.TeamRecord{LobbyID: fixture.aggregate.Lobby.ID}), &postgres.TeamRecord{}); count != 2 {
		t.Fatalf("stored teams = %d", count)
	}

	// A person on a removed team leaves it too.
	if _, err := changeTeams(t, fixture, []string{"Blue", "Gold", "Teal"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.Transact(context.Background(), fixture.aggregate.Lobby.Scope(), func(aggregate *domain.Aggregate) (domain.Change, error) {
		aggregate.Members[0].TeamID = &aggregate.Teams[2].ID
		return domain.Change{PublicChanged: true}, nil
	}); err != nil {
		t.Fatal(err)
	}
	unassigned, err := changeTeams(t, fixture, []string{"Teal", "Gold"}, func(aggregate *domain.Aggregate, _ []string) {
		aggregate.Members[0].TeamID = nil
	})
	if err != nil || unassigned.Members[0].TeamID != nil || !reflect.DeepEqual(teamNames(unassigned), []string{"Teal", "Gold"}) {
		t.Fatalf("unassigned = %#v, %#v, %v", unassigned.Members[0], unassigned.Teams, err)
	}
}

func TestStoreRefusesTeamChangesThatBreakTheRules(t *testing.T) {
	fixture := newStoreFixture(t)
	original := readStore(t, fixture)
	unchanged := func(what string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s was stored", what)
		}
		if current := readStore(t, fixture); !reflect.DeepEqual(current.Teams, original.Teams) || current.Lobby.Capacity != original.Lobby.Capacity || current.Lobby.Version != original.Lobby.Version {
			t.Fatalf("%s changed the lobby: %#v", what, current)
		}
	}

	// A member left on a removed team would point at nothing.
	_, err := changeTeams(t, fixture, []string{"Red", "Blue", "Green"}, func(aggregate *domain.Aggregate, _ []string) {
		aggregate.Members[0].TeamID = &aggregate.Teams[2].ID
	})
	if err != nil {
		t.Fatal(err)
	}
	original = readStore(t, fixture)
	_, err = changeTeams(t, fixture, []string{"Red", "Blue"}, nil)
	unchanged("a member on a removed team", err)

	_, err = fixture.store.Transact(context.Background(), fixture.aggregate.Lobby.Scope(), func(aggregate *domain.Aggregate) (domain.Change, error) {
		aggregate.Teams[0].ID = storeUUID(t)
		return domain.Change{PublicChanged: true}, nil
	})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatal("a team replaced at a kept position", err)
	}
	unchanged("a team replaced at a kept position", err)

	_, err = fixture.store.Transact(context.Background(), fixture.aggregate.Lobby.Scope(), func(aggregate *domain.Aggregate) (domain.Change, error) {
		aggregate.Teams = aggregate.Teams[:2]
		return domain.Change{PublicChanged: true}, nil
	})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatal("teams without the matching capacity", err)
	}
	unchanged("teams without the matching capacity", err)

	_, err = fixture.store.Transact(context.Background(), fixture.aggregate.Lobby.Scope(), func(aggregate *domain.Aggregate) (domain.Change, error) {
		aggregate.Lobby.Phase = domain.PhaseStarted
		aggregate.Lobby.GameSettings = json.RawMessage(`{"a":1}`)
		return domain.Change{PublicChanged: true}, nil
	})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatal("settings changed by a start", err)
	}
	unchanged("settings changed by a start", err)

	if _, err := fixture.store.Transact(context.Background(), fixture.aggregate.Lobby.Scope(), func(aggregate *domain.Aggregate) (domain.Change, error) {
		aggregate.Lobby.Phase = domain.PhaseStarted
		return domain.Change{PublicChanged: true}, nil
	}); err != nil {
		t.Fatal(err)
	}
	original = readStore(t, fixture)
	_, err = changeTeams(t, fixture, []string{"Red", "Blue", "Green", "Gold"}, nil)
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatal("teams of a started lobby", err)
	}
	unchanged("teams of a started lobby", err)
}
