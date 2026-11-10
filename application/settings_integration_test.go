package application_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"islandBattles/lobby/domain"
)

func teamDefinitions(names ...string) []domain.TeamDefinition {
	definitions := make([]domain.TeamDefinition, len(names))
	for index, name := range names {
		definitions[index] = domain.TeamDefinition{Name: name}
	}
	return definitions
}

func (fixture *serviceFixture) changeSettings(t *testing.T, settings string, names ...string) domain.Snapshot {
	t.Helper()
	snapshot, err := fixture.service.ChangeSettings(context.Background(), fixture.host, fixture.scope, domain.ChangeSettingsRequest{Teams: teamDefinitions(names...), GameSettings: json.RawMessage(settings)})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func snapshotTeamNames(teams []domain.Team) []string {
	names := make([]string, len(teams))
	for index, team := range teams {
		names[index] = team.Name
	}
	return names
}

func TestOnlyTheHostChangesSettingsWhileTheLobbyWaits(t *testing.T) {
	fixture := newServiceFixture(t, 2, quizLobbyWith(`{"teams":2}`))
	guest := fixture.player(t, "Guest")
	fixture.join(t, guest)
	outsider := fixture.player(t, "Outsider")
	request := domain.ChangeSettingsRequest{Teams: teamDefinitions("Blue", "Red", "Green"), GameSettings: json.RawMessage(`{"teams":3}`)}
	_, err := fixture.service.ChangeSettings(context.Background(), guest, fixture.scope, request)
	requireDomainError(t, err, domain.ErrNotHost)
	_, err = fixture.service.ChangeSettings(context.Background(), outsider, fixture.scope, request)
	requireDomainError(t, err, domain.ErrNotFound)

	// The teams and settings follow a create request's rules.
	for _, invalid := range [][]domain.TeamDefinition{teamDefinitions("Solo"), teamDefinitions("Blue", " blue "), teamDefinitions("Blue", "")} {
		_, err = fixture.service.ChangeSettings(context.Background(), fixture.host, fixture.scope, domain.ChangeSettingsRequest{Teams: invalid})
		requireDomainError(t, err, domain.ErrInvalidInput)
	}
	_, err = fixture.service.ChangeSettings(context.Background(), fixture.host, fixture.scope, domain.ChangeSettingsRequest{Teams: teamDefinitions("Blue", "Red"), GameSettings: json.RawMessage(`{"teams":3}`)})
	if domain.ErrorCodeOf(err) != domain.CodeInvalidGameSettings {
		t.Fatal("settings that don't fit the teams", err)
	}
	if current := fixture.read(t); current.Lobby.Version != 2 || string(current.Lobby.GameSettings) != `{"teams":2}` {
		t.Fatalf("refused changes changed the lobby: %#v", current.Lobby)
	}

	fixture.assign(t, fixture.host, 0, true)
	fixture.assign(t, guest, 1, true)
	if _, err := fixture.service.Start(context.Background(), fixture.host, fixture.scope); err != nil {
		t.Fatal(err)
	}
	_, err = fixture.service.ChangeSettings(context.Background(), fixture.host, fixture.scope, request)
	requireDomainError(t, err, domain.ErrPhaseConflict)
}

func TestChangingSettingsRebuildsTheTeamsByPosition(t *testing.T) {
	fixture := newServiceFixture(t, 2, quizLobbyWith(`{"teams":2}`))
	guest := fixture.player(t, "Guest")
	fixture.join(t, guest)
	fixture.assign(t, fixture.host, 0, true)
	fixture.assign(t, guest, 1, true)
	before := fixture.read(t)
	invalidations := fixture.pushed(invalidationPushes)

	// The first two teams swap names and keep their IDs and members; a third
	// is added.
	changed := fixture.changeSettings(t, `{ "teams": 3, "rounds": 4 }`, "Blue", "Red", "Green")
	if fixture.pushed(invalidationPushes) != invalidations+1 || changed.Lobby.Version != before.Lobby.Version+1 {
		t.Fatal("a change published no lobby.changed")
	}
	if !reflect.DeepEqual(snapshotTeamNames(changed.Teams), []string{"Blue", "Red", "Green"}) || changed.Teams[0].ID != before.Teams[0].ID || changed.Teams[1].ID != before.Teams[1].ID || changed.Teams[2].Order != 3 {
		t.Fatalf("teams = %#v", changed.Teams)
	}
	if changed.Lobby.Capacity != 3 || string(changed.Lobby.GameSettings) != `{"rounds":4,"teams":3}` {
		t.Fatalf("lobby = %#v", changed.Lobby)
	}
	for _, player := range []domain.Principal{fixture.host, guest} {
		member := memberFor(t, fixture.read(t), player.PlayerID)
		original := memberFor(t, before, player.PlayerID)
		if member.Ready || member.TeamID == nil || *member.TeamID != *original.TeamID || member.ID != original.ID {
			t.Fatalf("member after the change = %#v", member)
		}
	}

	// The lobby now holds a third person, and a start uses the new settings.
	third := fixture.player(t, "Third")
	fixture.join(t, third)
	fixture.initial = changed
	fixture.assign(t, third, 2, true)
	fixture.assign(t, fixture.host, 0, true)
	fixture.assign(t, guest, 1, true)
	started, err := fixture.service.Start(context.Background(), fixture.host, fixture.scope)
	if err != nil || len(started.StartRoster) != 3 || string(started.Lobby.GameSettings) != `{"rounds":4,"teams":3}` || started.StartRoster[2].TeamID != changed.Teams[2].ID {
		t.Fatalf("started = %#v, %v", started, err)
	}
}

func TestShrinkingTheTeamsUnassignsPeopleAndRemovesSeats(t *testing.T) {
	fixture := newServiceFixture(t, 4, seatLobby)
	guest := fixture.player(t, "Guest")
	fixture.join(t, guest)
	gatedGame.choose(fixture.scope.LobbyID, fixture.host.PlayerID, true)
	gatedGame.choose(fixture.scope.LobbyID, guest.PlayerID, true)
	fixture.assign(t, fixture.host, 0, true)
	fixture.assign(t, guest, 3, true)
	keptSeat := seats(fixture.addSeat(t, 1, `{"level":1}`))[0]
	removedSeat := seats(fixture.addSeat(t, 2, `{"level":2}`))[1]

	// Two people and the seat on the second team stay: two teams are too few.
	before := fixture.read(t)
	_, err := fixture.service.ChangeSettings(context.Background(), fixture.host, fixture.scope, domain.ChangeSettingsRequest{Teams: teamDefinitions("Red", "Blue")})
	requireDomainError(t, err, domain.ErrLobbyFull)
	if current := fixture.read(t); !reflect.DeepEqual(current.Teams, before.Teams) || current.Lobby.Version != before.Lobby.Version {
		t.Fatal("a refused change changed the lobby")
	}

	if _, err := fixture.service.RemoveSeat(context.Background(), fixture.host, fixture.scope, keptSeat.ID); err != nil {
		t.Fatal(err)
	}

	// The fourth team goes: its person stays without a team. The seat on the
	// third team keeps its team and stays ready.
	changed := fixture.changeSettings(t, ``, "Red", "Blue", "Gold")
	if !reflect.DeepEqual(snapshotTeamNames(changed.Teams), []string{"Red", "Blue", "Gold"}) || changed.Lobby.Capacity != 3 {
		t.Fatalf("teams = %#v", changed.Teams)
	}
	current := fixture.read(t)
	if guestMember := memberFor(t, current, guest.PlayerID); !guestMember.Active || guestMember.TeamID != nil || guestMember.Ready {
		t.Fatalf("the person on a removed team = %#v", guestMember)
	}
	if host := memberFor(t, current, fixture.host.PlayerID); host.TeamID == nil || *host.TeamID != before.Teams[0].ID || host.Ready {
		t.Fatalf("host = %#v", host)
	}
	if seat := memberFor(t, current, removedSeat.PlayerID); !seat.Active || !seat.Ready || seat.TeamID == nil || *seat.TeamID != before.Teams[2].ID {
		t.Fatalf("the seat on a kept team = %#v", seat)
	}

	// The third team goes: its seat is removed, as RemoveSeat removes one.
	shrunk := fixture.changeSettings(t, ``, "Red", "Blue")
	current = fixture.read(t)
	if seat := memberFor(t, current, removedSeat.PlayerID); seat.Active || seat.TeamID != nil || seat.Ready {
		t.Fatalf("the seat on a removed team = %#v", seat)
	}
	if len(seats(shrunk)) != 0 || len(current.Teams) != 2 || current.Lobby.Capacity != 2 || activeCount(current) != 2 {
		t.Fatalf("shrunk lobby = %#v", current)
	}
}

func TestMorePeopleThanTeamsIsLobbyFull(t *testing.T) {
	fixture := newServiceFixture(t, 3)
	fixture.join(t, fixture.player(t, "Guest"))
	fixture.join(t, fixture.player(t, "Third"))
	_, err := fixture.service.ChangeSettings(context.Background(), fixture.host, fixture.scope, domain.ChangeSettingsRequest{Teams: teamDefinitions("Red", "Blue")})
	requireDomainError(t, err, domain.ErrLobbyFull)
	if current := fixture.read(t); len(current.Teams) != 3 || current.Lobby.Capacity != 3 {
		t.Fatalf("a refused change changed the teams: %#v", current.Teams)
	}
}

func TestUnchangedSettingsChangeNothing(t *testing.T) {
	fixture := newServiceFixture(t, 2, quizLobbyWith(`{"rounds":2}`))
	guest := fixture.player(t, "Guest")
	fixture.join(t, guest)
	fixture.assign(t, fixture.host, 0, true)
	fixture.assign(t, guest, 1, true)
	before := fixture.read(t)
	invalidations := fixture.pushed(invalidationPushes)
	snapshot := fixture.changeSettings(t, `{ "rounds" : 2 }`, " Red ", "Blue")
	if fixture.pushed(invalidationPushes) != invalidations || snapshot.Lobby.Version != before.Lobby.Version {
		t.Fatal("an unchanged call published a change")
	}
	if current := fixture.read(t); !memberFor(t, current, guest.PlayerID).Ready || !memberFor(t, current, fixture.host.PlayerID).Ready {
		t.Fatal("an unchanged call cleared ready")
	}
	// A renamed team alone is a change.
	renamed := fixture.changeSettings(t, `{"rounds":2}`, "Red", "Navy")
	if renamed.Lobby.Version != before.Lobby.Version+1 || memberFor(t, fixture.read(t), guest.PlayerID).Ready {
		t.Fatal("renaming a team was no change")
	}
}
