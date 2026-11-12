package application_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"islandBattles/lobby/domain"
)

// choiceSettingsGame stands in for a game whose people choose something in
// their member settings, {"choice": "rock"} or {"choice": "paper"}, and must
// choose before getting ready. It records the lobbies it was asked about.
type choiceSettingsGame struct {
	mutex   sync.Mutex
	lobbies []string
}

var settingsGame = &choiceSettingsGame{}

const choiceMissingProblem = "urn:example:problem:choice-missing"

func decodeChoice(settings json.RawMessage) (string, error) {
	var decoded struct {
		Choice string `json:"choice"`
	}
	decoder := json.NewDecoder(bytes.NewReader(settings))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil || (decoded.Choice != "rock" && decoded.Choice != "paper") {
		return "", errors.New("choice must be rock or paper")
	}
	return decoded.Choice, nil
}

func (game *choiceSettingsGame) validate(_ context.Context, lobbyID string, settings json.RawMessage) error {
	game.mutex.Lock()
	game.lobbies = append(game.lobbies, lobbyID)
	game.mutex.Unlock()
	_, err := decodeChoice(settings)
	return err
}

func (game *choiceSettingsGame) askedAbout(lobbyID string) bool {
	game.mutex.Lock()
	defer game.mutex.Unlock()
	for _, asked := range game.lobbies {
		if asked == lobbyID {
			return true
		}
	}
	return false
}

// gate decides from the members' settings alone.
func (*choiceSettingsGame) gate(context.Context, string) (domain.ReadyCheck, error) {
	return func(members []domain.ReadyMember, atStart bool) error {
		var missing []string
		for _, member := range members {
			if _, err := decodeChoice(member.Settings); err != nil {
				missing = append(missing, member.DisplayName)
			}
		}
		if len(missing) == 0 {
			return nil
		}
		if atStart {
			return domain.GameRequirement(choiceMissingProblem, "Waiting for "+strings.Join(missing, ", ")+" to choose.")
		}
		return domain.GameRequirement(choiceMissingProblem, "Choose before getting ready.")
	}, nil
}

func settingsLobby(request *domain.CreateRequest) { request.GameKey = "settings-game" }

func (fixture *serviceFixture) setMemberSettings(principal domain.Principal, settings string) (domain.Snapshot, error) {
	return fixture.service.SetMemberSettings(context.Background(), principal, fixture.scope, domain.MemberSettingsRequest{MemberSettings: json.RawMessage(settings)})
}

func TestMemberSettingsAreYourOwnAndEveryMemberSeesThem(t *testing.T) {
	fixture := newServiceFixture(t, 2, settingsLobby)
	guest := fixture.player(t, "Guest")
	fixture.join(t, guest)
	before := fixture.read(t)
	pushes := fixture.pushed(invalidationPushes)

	snapshot, err := fixture.setMemberSettings(fixture.host, ` { "choice" : "rock" } `)
	if err != nil {
		t.Fatal(err)
	}
	if !settingsGame.askedAbout(fixture.scope.LobbyID) {
		t.Fatal("the game was not asked about the lobby's member settings")
	}
	if snapshot.Lobby.Version != before.Lobby.Version+1 || fixture.pushed(invalidationPushes) != pushes+1 {
		t.Fatalf("setting member settings = version %d after %d, %d pushes after %d", snapshot.Lobby.Version, before.Lobby.Version, fixture.pushed(invalidationPushes), pushes)
	}
	// The guest sees the host's settings, and has none of their own.
	seen, err := fixture.service.Snapshot(context.Background(), guest, fixture.scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range seen.Members {
		want := ""
		if member.PlayerID == fixture.host.PlayerID {
			want = `{"choice":"rock"}`
		}
		if string(member.MemberSettings) != want {
			t.Fatalf("%s's member settings = %s, want %q", member.DisplayName, member.MemberSettings, want)
		}
	}

	// Only a member sets settings, and only their own.
	outsider := fixture.player(t, "Outsider")
	_, err = fixture.setMemberSettings(outsider, `{"choice":"paper"}`)
	requireDomainError(t, err, domain.ErrNotFound)
	if _, err := fixture.setMemberSettings(guest, `{"choice":"paper"}`); err != nil {
		t.Fatal(err)
	}
	aggregate := fixture.read(t)
	if host, member := memberFor(t, aggregate, fixture.host.PlayerID), memberFor(t, aggregate, guest.PlayerID); string(host.MemberSettings) != `{"choice":"rock"}` || string(member.MemberSettings) != `{"choice":"paper"}` {
		t.Fatalf("stored settings = host %s, guest %s", host.MemberSettings, member.MemberSettings)
	}
}

func TestInvalidMemberSettingsChangeNothing(t *testing.T) {
	fixture := newServiceFixture(t, 2, settingsLobby)
	before := fixture.read(t)
	pushes := fixture.pushed(invalidationPushes)
	invalid := map[string]string{
		`{"choice":"scissors"}`:       "choice must be rock or paper",
		`{"choice":"rock","extra":1}`: "choice must be rock or paper",
		`["rock"]`:                    "member settings must be a JSON object",
		``:                            "member settings must be a JSON object",
		`{"choice":"rock"} {}`:        "member settings must be a single JSON object",
		`{"choice":"` + strings.Repeat("x", 4*1024) + `"}`:         "member settings are too large",
		`{"choice":"rock","note":"` + "\\u0000" + `"}`:             "member settings must not contain NUL characters",
		`{"choice":"rock","note":"` + strings.Repeat("y", 8) + `"`: "member settings must be a JSON object",
	}
	for settings, message := range invalid {
		_, err := fixture.setMemberSettings(fixture.host, settings)
		var domainError *domain.Error
		if !errors.As(err, &domainError) || domainError.Code != domain.CodeInvalidMemberSettings || domainError.Message != message {
			t.Fatalf("settings %.40q = %v; want invalid_member_settings %q", settings, err, message)
		}
	}
	if after := fixture.read(t); !reflect.DeepEqual(before, after) || fixture.pushed(invalidationPushes) != pushes {
		t.Fatal("invalid member settings changed the lobby")
	}

	// A game without a validator takes no member settings.
	plain := newServiceFixture(t, 2)
	_, err := plain.setMemberSettings(plain.host, `{"choice":"rock"}`)
	var domainError *domain.Error
	if !errors.As(err, &domainError) || domainError.Code != domain.CodeInvalidMemberSettings || domainError.Message != "this game takes no member settings" {
		t.Fatalf("member settings for a game without them = %v", err)
	}
}

func TestMemberSettingsClearYourReadyUnlessUnchanged(t *testing.T) {
	fixture := newServiceFixture(t, 2, settingsLobby)
	guest := fixture.player(t, "Guest")
	fixture.join(t, guest)

	// The game's gate reads the settings: no choice, no ready.
	team := fixture.initial.Teams[0].ID
	if _, err := fixture.service.SelectTeam(context.Background(), fixture.host, fixture.scope, domain.SelectTeamRequest{TeamID: &team}); err != nil {
		t.Fatal(err)
	}
	_, err := fixture.service.SetReady(context.Background(), fixture.host, fixture.scope, domain.ReadyRequest{Ready: true})
	var domainError *domain.Error
	if !errors.As(err, &domainError) || domainError.Code != domain.CodeGameRequirement || domainError.Type != choiceMissingProblem {
		t.Fatalf("ready without settings = %v", err)
	}
	if _, err := fixture.setMemberSettings(fixture.host, `{"choice":"rock"}`); err != nil {
		t.Fatal(err)
	}
	fixture.assign(t, fixture.host, 0, true)
	if _, err := fixture.setMemberSettings(guest, `{"choice":"paper"}`); err != nil {
		t.Fatal(err)
	}
	fixture.assign(t, guest, 1, true)

	// The same settings again change nothing: the version stays, and the
	// host stays ready.
	before := fixture.read(t)
	pushes := fixture.pushed(invalidationPushes)
	unchanged, err := fixture.setMemberSettings(fixture.host, `{ "choice": "rock" }`)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Lobby.Version != before.Lobby.Version || !memberFor(t, fixture.read(t), fixture.host.PlayerID).Ready || fixture.pushed(invalidationPushes) != pushes {
		t.Fatalf("unchanged settings = version %d after %d, host ready %v", unchanged.Lobby.Version, before.Lobby.Version, memberFor(t, fixture.read(t), fixture.host.PlayerID).Ready)
	}

	// New settings clear the host's ready, and only theirs.
	changed, err := fixture.setMemberSettings(fixture.host, `{"choice":"paper"}`)
	if err != nil {
		t.Fatal(err)
	}
	aggregate := fixture.read(t)
	if changed.Lobby.Version != before.Lobby.Version+1 || memberFor(t, aggregate, fixture.host.PlayerID).Ready || !memberFor(t, aggregate, guest.PlayerID).Ready {
		t.Fatalf("changed settings = version %d, host ready %v, guest ready %v", changed.Lobby.Version, memberFor(t, aggregate, fixture.host.PlayerID).Ready, memberFor(t, aggregate, guest.PlayerID).Ready)
	}
	_, err = fixture.service.Start(context.Background(), fixture.host, fixture.scope)
	requireDomainError(t, err, domain.ErrNotReady)
}

func TestMemberSettingsAreSetOnlyWhileWaitingAndKeptForTheNextRound(t *testing.T) {
	fixture := newServiceFixture(t, 2, settingsLobby)
	guest := fixture.player(t, "Guest")
	fixture.join(t, guest)
	if _, err := fixture.setMemberSettings(fixture.host, `{"choice":"rock"}`); err != nil {
		t.Fatal(err)
	}

	// The start checks every person's settings again.
	fixture.assign(t, fixture.host, 0, true)
	team := fixture.initial.Teams[1].ID
	if _, err := fixture.service.SelectTeam(context.Background(), guest, fixture.scope, domain.SelectTeamRequest{TeamID: &team}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.setMemberSettings(guest, `{"choice":"paper"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.SetReady(context.Background(), guest, fixture.scope, domain.ReadyRequest{Ready: true}); err != nil {
		t.Fatal(err)
	}
	started, err := fixture.service.Start(context.Background(), fixture.host, fixture.scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range started.Members {
		if len(member.MemberSettings) == 0 {
			t.Fatalf("the started snapshot lost %s's settings", member.DisplayName)
		}
	}

	before := fixture.read(t)
	_, err = fixture.setMemberSettings(fixture.host, `{"choice":"paper"}`)
	requireDomainError(t, err, domain.ErrPhaseConflict)
	if !reflect.DeepEqual(before, fixture.read(t)) {
		t.Fatal("member settings changed a started lobby")
	}

	// Play again: the settings stay, and every person gets ready again.
	replayGame.end(fixture.scope.LobbyID, 1)
	reopened, err := fixture.service.Reopen(context.Background(), guest, fixture.scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range reopened.Members {
		want := map[string]string{fixture.host.PlayerID: `{"choice":"rock"}`, guest.PlayerID: `{"choice":"paper"}`}[member.PlayerID]
		if string(member.MemberSettings) != want || member.Ready {
			t.Fatalf("after reopening, %s has settings %s and ready %v", member.DisplayName, member.MemberSettings, member.Ready)
		}
	}
	fixture.assign(t, fixture.host, 0, true)
	fixture.assign(t, guest, 1, true)
	if _, err := fixture.service.Start(context.Background(), fixture.host, fixture.scope); err != nil {
		t.Fatalf("starting the next round with the kept settings: %v", err)
	}
}

func TestMemberSettingsStayWhenAPersonLeavesAndRejoins(t *testing.T) {
	fixture := newServiceFixture(t, 2, settingsLobby)
	guest := fixture.player(t, "Guest")
	fixture.join(t, guest)
	if _, err := fixture.setMemberSettings(guest, `{"choice":"paper"}`); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.Leave(context.Background(), guest, fixture.scope); err != nil {
		t.Fatal(err)
	}
	rejoined, _ := fixture.join(t, guest)
	for _, member := range rejoined.Members {
		if member.PlayerID == guest.PlayerID && string(member.MemberSettings) != `{"choice":"paper"}` {
			t.Fatalf("the rejoined guest's settings = %s", member.MemberSettings)
		}
	}
}
