package application_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"islandBattles/lobby/domain"
	"islandBattles/lobby/postgres"
)

// botSeat stands in for a game's seat validator: a seat takes a bot level
// from 1 to 3 and nothing else.
func botSeat(_ context.Context, settings json.RawMessage) (string, error) {
	var decoded struct {
		Level int `json:"level"`
	}
	decoder := json.NewDecoder(bytes.NewReader(settings))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil || decoded.Level < 1 || decoded.Level > 3 {
		return "", errors.New("level must be 1, 2 or 3")
	}
	return fmt.Sprintf("Bot (level %d)", decoded.Level), nil
}

func seatLobby(request *domain.CreateRequest) { request.GameKey = "seat-game" }

func (fixture *serviceFixture) addSeat(t *testing.T, teamIndex int, settings string) domain.Snapshot {
	t.Helper()
	snapshot, err := fixture.service.AddSeat(context.Background(), fixture.host, fixture.scope, domain.AddSeatRequest{TeamID: fixture.initial.Teams[teamIndex].ID, SeatSettings: json.RawMessage(settings)})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func seats(snapshot domain.Snapshot) []domain.Member {
	var found []domain.Member
	for _, member := range snapshot.Members {
		if member.IsSeat() {
			found = append(found, member)
		}
	}
	return found
}

func TestHostPlaysAloneAgainstASeat(t *testing.T) {
	fixture := newServiceFixture(t, 2, seatLobby)
	invalidations := fixture.pushed(invalidationPushes)
	snapshot := fixture.addSeat(t, 1, `{ "level": 2 }`)
	if fixture.pushed(invalidationPushes) != invalidations+1 {
		t.Fatal("adding a seat published no lobby.changed")
	}
	added := seats(snapshot)
	if len(added) != 1 || added[0].DisplayName != "Bot (level 2)" || string(added[0].SeatSettings) != `{"level":2}` || !added[0].Ready || !added[0].Active ||
		added[0].TeamID == nil || *added[0].TeamID != fixture.initial.Teams[1].ID || added[0].PlayerID == "" || added[0].DisconnectedUntil != nil {
		t.Fatalf("seat = %#v", added)
	}
	var player postgres.PlayerRecord
	if err := fixture.db.First(&player, "id = ?", added[0].PlayerID).Error; err != nil || player.DisplayName != "Bot (level 2)" {
		t.Fatalf("the seat's player = %#v, %v", player, err)
	}

	// Leases end without the seat, which never disconnects: the host
	// heartbeats past the time a member without a lease would have expired.
	fixture.clock.Set(fixture.origin.Add(30 * time.Second))
	if _, err := fixture.service.Heartbeat(context.Background(), fixture.host, fixture.scope, domain.HeartbeatRequest{ClientInstanceID: fixture.clientID, MembershipGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	fixture.clock.Set(fixture.origin.Add(70 * time.Second))
	if _, err := fixture.service.Heartbeat(context.Background(), fixture.host, fixture.scope, domain.HeartbeatRequest{ClientInstanceID: fixture.clientID, MembershipGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	fixture.clock.Set(fixture.origin.Add(110 * time.Second))
	gatedGame.choose(fixture.scope.LobbyID, fixture.host.PlayerID, true)
	fixture.assign(t, fixture.host, 0, true)

	// The game's ready gate asks only the person: a seat makes no choice.
	started, err := fixture.service.Start(context.Background(), fixture.host, fixture.scope)
	if err != nil || started.Lobby.Phase != domain.PhaseStarted || len(started.StartRoster) != 2 {
		t.Fatal("the host and a seat did not start", err)
	}
	seat := seats(started)[0]
	entry := started.StartRoster[1]
	if entry.PlayerID != seat.PlayerID || entry.MemberID != seat.ID || entry.TeamID != fixture.initial.Teams[1].ID || entry.TeamOrder != 2 {
		t.Fatalf("roster = %#v, seat = %#v", started.StartRoster, seat)
	}
	if string(seat.SeatSettings) != `{"level":2}` {
		t.Fatalf("the started snapshot's seat settings = %s", seat.SeatSettings)
	}
}

func TestOnlyTheHostChangesSeatsWhileTheLobbyWaits(t *testing.T) {
	fixture := newServiceFixture(t, 4, seatLobby)
	guest := fixture.player(t, "Guest")
	fixture.join(t, guest)
	outsider := fixture.player(t, "Outsider")
	request := domain.AddSeatRequest{TeamID: fixture.initial.Teams[1].ID, SeatSettings: json.RawMessage(`{"level":1}`)}
	_, err := fixture.service.AddSeat(context.Background(), guest, fixture.scope, request)
	requireDomainError(t, err, domain.ErrNotHost)
	_, err = fixture.service.AddSeat(context.Background(), outsider, fixture.scope, request)
	requireDomainError(t, err, domain.ErrNotFound)

	seat := seats(fixture.addSeat(t, 1, `{"level":1}`))[0]
	_, err = fixture.service.RemoveSeat(context.Background(), guest, fixture.scope, seat.ID)
	requireDomainError(t, err, domain.ErrNotHost)
	// A person is not a seat, and an unknown ID names none.
	host := memberFor(t, fixture.read(t), fixture.host.PlayerID)
	for _, id := range []string{host.ID, integrationID(t)} {
		_, err = fixture.service.RemoveSeat(context.Background(), fixture.host, fixture.scope, id)
		requireDomainError(t, err, domain.ErrInvalidInput)
	}
	invalidations := fixture.pushed(invalidationPushes)
	removed, err := fixture.service.RemoveSeat(context.Background(), fixture.host, fixture.scope, seat.ID)
	if err != nil || len(seats(removed)) != 0 || fixture.pushed(invalidationPushes) != invalidations+1 {
		t.Fatal("removing the seat", err, removed.Members)
	}
	before := fixture.read(t)
	if _, err := fixture.service.RemoveSeat(context.Background(), fixture.host, fixture.scope, seat.ID); err != nil || fixture.read(t).Lobby.Version != before.Lobby.Version {
		t.Fatal("removing a removed seat again changed the lobby", err)
	}

	// Once the lobby starts, the seats are fixed.
	kept := seats(fixture.addSeat(t, 1, `{"level":3}`))[0]
	gatedGame.choose(fixture.scope.LobbyID, fixture.host.PlayerID, true)
	gatedGame.choose(fixture.scope.LobbyID, guest.PlayerID, true)
	fixture.assign(t, fixture.host, 0, true)
	fixture.assign(t, guest, 1, true)
	if _, err := fixture.service.Start(context.Background(), fixture.host, fixture.scope); err != nil {
		t.Fatal(err)
	}
	_, err = fixture.service.AddSeat(context.Background(), fixture.host, fixture.scope, request)
	requireDomainError(t, err, domain.ErrPhaseConflict)
	_, err = fixture.service.RemoveSeat(context.Background(), fixture.host, fixture.scope, kept.ID)
	requireDomainError(t, err, domain.ErrPhaseConflict)
}

func TestSeatsCountTowardCapacityAndTheGameChecksTheirSettings(t *testing.T) {
	fixture := newServiceFixture(t, 3, seatLobby)
	add := func(teamID, settings string) error {
		_, err := fixture.service.AddSeat(context.Background(), fixture.host, fixture.scope, domain.AddSeatRequest{TeamID: teamID, SeatSettings: json.RawMessage(settings)})
		return err
	}
	red, blue := fixture.initial.Teams[0].ID, fixture.initial.Teams[1].ID
	for _, invalid := range []string{`{"level":4}`, `{"level":1,"extra":true}`, `[1]`, `"text"`, ``} {
		err := add(blue, invalid)
		if domain.ErrorCodeOf(err) != domain.CodeInvalidSeatSettings {
			t.Fatalf("settings %q = %v", invalid, err)
		}
	}
	requireDomainError(t, add(integrationID(t), `{"level":1}`), domain.ErrInvalidInput)
	if err := add(red, `{"level":1}`); err != nil {
		t.Fatal(err)
	}
	// Red holds one, and the seat took it.
	requireDomainError(t, add(red, `{"level":1}`), domain.ErrTeamFull)
	snapshot := fixture.addSeat(t, 1, `{"level":2}`)
	// Two seats and the host fill the lobby of three.
	requireDomainError(t, add(blue, `{"level":2}`), domain.ErrLobbyFull)
	_, err := fixture.service.Join(context.Background(), fixture.player(t, "Late"), domain.JoinRequest{GameKey: fixture.scope.GameKey, JoinCode: fixture.initial.Lobby.JoinCode, ClientInstanceID: integrationID(t)})
	requireDomainError(t, err, domain.ErrLobbyFull)
	if names := []string{seats(snapshot)[0].DisplayName, seats(snapshot)[1].DisplayName}; names[0] != "Bot (level 1)" || names[1] != "Bot (level 2)" {
		t.Fatalf("seat names = %v", names)
	}
}

func TestSeatsOfOneSettingGetDistinctNames(t *testing.T) {
	fixture := newServiceFixture(t, 4, seatLobby)
	fixture.addSeat(t, 1, `{"level":2}`)
	snapshot := fixture.addSeat(t, 1, `{"level":2}`)
	if names := []string{seats(snapshot)[0].DisplayName, seats(snapshot)[1].DisplayName}; names[0] != "Bot (level 2)" || names[1] != "Bot (level 2) #2" {
		t.Fatalf("seat names = %v", names)
	}
}

func TestSeatsAloneNeitherHostNorStart(t *testing.T) {
	fixture := newServiceFixture(t, 3, seatLobby)
	fixture.addSeat(t, 0, `{"level":1}`)
	fixture.addSeat(t, 1, `{"level":2}`)
	// Every seat is ready, but the host isn't, so the lobby can't start.
	_, err := fixture.service.Start(context.Background(), fixture.host, fixture.scope)
	requireDomainError(t, err, domain.ErrNotReady)
	// Without its last person, the lobby has no host and closes: seats never
	// host, so two seats and no person never start.
	if err := fixture.service.Leave(context.Background(), fixture.host, fixture.scope); err != nil {
		t.Fatal(err)
	}
	after := fixture.read(t)
	if after.Lobby.HostPlayerID != nil || after.Lobby.Phase != domain.PhaseClosed {
		t.Fatalf("a lobby of seats alone = host %v, phase %s", after.Lobby.HostPlayerID, after.Lobby.Phase)
	}
}

func TestGamesWithoutSeatsRefuseEverySeat(t *testing.T) {
	fixture := newServiceFixture(t, 3, quizLobbyWith(`{}`))
	_, err := fixture.service.AddSeat(context.Background(), fixture.host, fixture.scope, domain.AddSeatRequest{TeamID: fixture.initial.Teams[1].ID, SeatSettings: json.RawMessage(`{"bot_level":2}`)})
	var refusal *domain.Error
	if !errors.As(err, &refusal) || refusal.Code != domain.CodeInvalidSeatSettings || refusal.Message != "this game has no seats that the host can add" {
		t.Fatalf("quiz-demo seat = %v", err)
	}
	if len(fixture.read(t).Members) != 1 {
		t.Fatal("a refused seat was added")
	}
}
