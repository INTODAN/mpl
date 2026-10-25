package domain_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"islandBattles/lobby/domain"
)

const roomID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
const playerID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"

func pointer[T any](value T) *T { return &value }
func TestChannelCanonicalRoundTrip(t *testing.T) {
	for _, game := range []string{"island-battles", "quiz-demo", "a", "a0-"} {
		scope := domain.Scope{GameKey: game, LobbyID: roomID}
		channel, err := domain.RoomChannel(scope)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := domain.ParseChannel(channel)
		if err != nil || parsed.Namespace != domain.ChannelLobby || parsed.Scope != scope {
			t.Fatalf("roundtrip: %#v %v", parsed, err)
		}
		rebuilt, err := domain.RoomChannel(parsed.Scope)
		if err != nil || rebuilt != channel {
			t.Fatal("noncanonical roundtrip")
		}
	}
	channel, err := domain.PlayerChannel(playerID)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := domain.ParseChannel(channel)
	if err != nil || parsed.PlayerID != playerID || parsed.Namespace != domain.ChannelPlayer {
		t.Fatalf("player: %#v %v", parsed, err)
	}
	rebuilt, err := domain.PlayerChannel(parsed.PlayerID)
	if err != nil || rebuilt != channel {
		t.Fatal("noncanonical player roundtrip")
	}
}
func TestChannelRejectsAmbiguousIdentifiers(t *testing.T) {
	for _, value := range []string{"", ":" + playerID, "unknown:" + playerID, "player:", "player:" + playerID + ":x", "player:" + strings.ToUpper(playerID), "player:" + playerID + "#x", "player:$" + playerID, "player:/" + playerID, "player:%62" + playerID[1:], "player:123", "lobby:Quiz:" + roomID, "lobby:1quiz:" + roomID, "lobby:quiz_demo:" + roomID, "lobby::" + roomID, "lobby:" + strings.Repeat("a", 33) + ":" + roomID, "lobby:quiz:" + roomID + ":x", "lobby:quiz#x:" + roomID, "lobby:quiz/:" + roomID} {
		t.Run(value, func(t *testing.T) {
			if _, err := domain.ParseChannel(value); !errors.Is(err, domain.ErrInvalidInput) {
				t.Fatalf("accepted %q", value)
			}
		})
	}
}
func TestJoinCodeNormalization(t *testing.T) {
	normalized, err := domain.NormalizeJoinCode(" \t\nabcd2345\r\v\f")
	if err != nil || normalized != "ABCD2345" {
		t.Fatalf("%q %v", normalized, err)
	}
	for _, value := range []string{"ABCD234", "ABCD23456", "ABCD2340", "ABCD2341", "ABCD234I", "ABCD234O", "ＡBCD2345", "\u00a0ABCD2345", "ABCD 345", "éBCD2345"} {
		if _, err := domain.NormalizeJoinCode(value); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
}
func TestPolicyValidation(t *testing.T) {
	valid := domain.DefaultPolicy(domain.GameRegistration{Key: "island-battles"}, domain.GameRegistration{Key: "quiz-demo"})
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	if valid.HeartbeatInterval != 15*time.Second || valid.LeaseDuration != 45*time.Second || valid.GraceDuration != 60*time.Second || valid.SessionDuration != 24*time.Hour || valid.ConnectionTokenDuration != 5*time.Minute {
		t.Fatal("incorrect defaults")
	}
	if err := (domain.Policy{}).Validate(); err == nil {
		t.Fatal("zero policy accepted")
	}
	for _, mutate := range []func(*domain.Policy){func(p *domain.Policy) { p.Games = nil }, func(p *domain.Policy) { p.Games = []domain.GameRegistration{{Key: "quiz"}, {Key: "quiz"}} }, func(p *domain.Policy) { p.Games = []domain.GameRegistration{{Key: "Quiz"}} }, func(p *domain.Policy) { p.HeartbeatInterval = 0 }, func(p *domain.Policy) { p.LeaseDuration = p.HeartbeatInterval }, func(p *domain.Policy) { p.GraceDuration = -time.Second }, func(p *domain.Policy) { p.SessionDuration = 0 }, func(p *domain.Policy) { p.ConnectionTokenDuration = p.SessionDuration + time.Second }, func(p *domain.Policy) { p.SubscriptionDuration = 0 }} {
		invalid := valid
		mutate(&invalid)
		if err := invalid.Validate(); err == nil {
			t.Errorf("accepted invalid policy %#v", invalid)
		}
	}
	if valid.ValidateGame("quiz-demo") != nil || valid.ValidateGame("unregistered") == nil {
		t.Fatal("game registration ignored")
	}
	if game, err := valid.Game("quiz-demo"); err != nil || game.Key != "quiz-demo" {
		t.Fatalf("game lookup = %#v %v", game, err)
	}
}
func TestNormalizeGameSettings(t *testing.T) {
	for input, want := range map[string]string{
		"":      "{}",
		"{}":    "{}",
		" { } ": "{}",
		`{"b": 1, "a": {"z": [1, 2.50], "y": null}}`: `{"a":{"y":null,"z":[1,2.50]},"b":1}`,
		`{"big": 12345678901234567890123}`:           `{"big":12345678901234567890123}`,
		`{"html": "<b>&</b>"}`:                       `{"html":"<b>&</b>"}`,
	} {
		normalized, err := domain.NormalizeGameSettings(json.RawMessage(input))
		if err != nil || string(normalized) != want {
			t.Errorf("NormalizeGameSettings(%q) = %q, %v; want %q", input, normalized, err, want)
		}
	}
	tooLarge := `{"value":"` + strings.Repeat("a", domain.MaxGameSettingsBytes) + `"}`
	for _, input := range []string{"null", "[]", `"text"`, "1", "{", `{"a":1} {}`, `{"a":1}x`, tooLarge, `{"note":"a\u0000b"}`, `{"a\u0000":1}`, `{"list":[{"deep":"\u0000"}]}`} {
		if _, err := domain.NormalizeGameSettings(json.RawMessage(input)); !errors.Is(err, domain.NewError(domain.CodeInvalidGameSettings, "")) {
			t.Errorf("NormalizeGameSettings(%.20q) error = %v", input, err)
		}
	}
}
func TestValidateGameSettingsUsesTheGameValidator(t *testing.T) {
	var received string
	var receivedTeams []domain.TeamDefinition
	policy := domain.DefaultPolicy(
		domain.GameRegistration{Key: "no-settings"},
		domain.GameRegistration{Key: "strict", ValidateSettings: func(_ context.Context, settings json.RawMessage, teams []domain.TeamDefinition) error {
			received, receivedTeams = string(settings), teams
			if !strings.Contains(received, `"rounds"`) {
				return errors.New("rounds is required")
			}
			return nil
		}},
		domain.GameRegistration{Key: "offline", ValidateSettings: func(context.Context, json.RawMessage, []domain.TeamDefinition) error {
			return &domain.Error{Code: domain.CodeUnavailable, Message: "map store offline", Cause: errors.New("connection refused")}
		}},
	)
	ctx := context.Background()
	teams := []domain.TeamDefinition{{Name: "Red", Capacity: 1}, {Name: "Blue", Capacity: 1}}
	if settings, err := policy.ValidateGameSettings(ctx, "no-settings", nil, teams); err != nil || string(settings) != "{}" {
		t.Fatalf("empty settings = %q, %v", settings, err)
	}
	if _, err := policy.ValidateGameSettings(ctx, "no-settings", json.RawMessage(`{"rounds":3}`), teams); domain.ErrorCodeOf(err) != domain.CodeInvalidGameSettings {
		t.Fatalf("a game without a validator accepted settings: %v", err)
	}
	settings, err := policy.ValidateGameSettings(ctx, "strict", json.RawMessage(`{ "rounds": 3, "mode": "fast" }`), teams)
	if err != nil || string(settings) != `{"mode":"fast","rounds":3}` || received != string(settings) || !reflect.DeepEqual(receivedTeams, teams) {
		t.Fatalf("validated settings = %q, %v; validator saw %q with teams %v", settings, err, received, receivedTeams)
	}
	_, err = policy.ValidateGameSettings(ctx, "strict", nil, teams)
	if domain.ErrorCodeOf(err) != domain.CodeInvalidGameSettings || err.Error() != "rounds is required" {
		t.Fatalf("rejection = %v", err)
	}
	if _, err := policy.ValidateGameSettings(ctx, "offline", nil, teams); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("validator outage = %v", err)
	}
	if _, err := policy.ValidateGameSettings(ctx, "unregistered", nil, teams); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("unregistered game = %v", err)
	}
}
func TestTeamDefinitionValidation(t *testing.T) {
	valid := []domain.TeamDefinition{{Name: "  אדום ", Capacity: 2}, {Name: "青", Capacity: 2}}
	normalized, err := domain.ValidateTeamDefinitions(valid, 4)
	if err != nil || normalized[0].Name != "אדום" || valid[0].Name != "  אדום " {
		t.Fatalf("labels mutated/lost: %#v %v", normalized, err)
	}
	for _, definitions := range [][]domain.TeamDefinition{
		{}, {{Name: "a", Capacity: 2}}, make([]domain.TeamDefinition, 17),
		{{Name: " red ", Capacity: 2}, {Name: "RED", Capacity: 2}},
		{{Name: "Straße", Capacity: 2}, {Name: "STRASSE", Capacity: 2}},
		{{Name: "a", Capacity: 0}, {Name: "b", Capacity: 4}},
		{{Name: "a", Capacity: 65}, {Name: "b", Capacity: 4}},
		{{Name: "a", Capacity: 1}, {Name: "b", Capacity: 1}},
		{{Name: " ", Capacity: 4}, {Name: "b", Capacity: 4}},
		{{Name: strings.Repeat("界", 41), Capacity: 4}, {Name: "b", Capacity: 4}},
	} {
		if _, err := domain.ValidateTeamDefinitions(definitions, 4); err == nil {
			t.Fatalf("accepted %#v", definitions)
		}
	}
}
func TestHostElectionDeterministic(t *testing.T) {
	members := []domain.Member{{ID: "0", PlayerID: "inactive", JoinSequence: 1}, {ID: "z", PlayerID: "late", JoinSequence: 3, Active: true}, {ID: "b", PlayerID: "second", JoinSequence: 2, Active: true}, {ID: "a", PlayerID: "first", JoinSequence: 2, Active: true}}
	host := domain.ElectHost(members)
	if host == nil || *host != "first" {
		t.Fatalf("host %v", host)
	}
	*host = "mutated"
	if members[3].PlayerID != "first" {
		t.Fatal("host pointer aliases member")
	}
	if domain.ElectHost(nil) != nil || domain.ElectHost([]domain.Member{{Active: false}}) != nil {
		t.Fatal("elected inactive host")
	}
}
func TestEffectiveLeaseExpiry(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	early := now.Add(-time.Minute)
	late := now.Add(time.Minute)
	for _, test := range []struct {
		released *time.Time
		want     time.Time
	}{{nil, now}, {&early, early}, {&late, now}} {
		if got := domain.EffectiveLeaseExpiry(domain.Lease{ExpiresAt: now, ReleasedAt: test.released}); !got.Equal(test.want) {
			t.Fatalf("got %v want %v", got, test.want)
		}
	}
	leases := []domain.Lease{{PlayerID: playerID, Generation: 1, ExpiresAt: late, ReleasedAt: &early}, {PlayerID: playerID, Generation: 1, ExpiresAt: now, ReleasedAt: &late}, {PlayerID: playerID, Generation: 2, ExpiresAt: late}}
	latest, found := domain.LatestEffectiveLeaseExpiry(leases, playerID, 1)
	if !found || !latest.Equal(now) {
		t.Fatal("released max wrong", latest)
	}
	if domain.HasLiveLease(leases, playerID, 1, early) {
		t.Fatal("released lease is live")
	}
	if domain.HasLiveLease(leases, playerID, 2, late) || !domain.HasLiveLease(leases, playerID, 2, late.Add(-time.Nanosecond)) {
		t.Fatal("lease boundary is not strict")
	}
	leases = append(leases, domain.Lease{PlayerID: playerID, Generation: 1, ExpiresAt: late})
	latest, found = domain.LatestEffectiveLeaseExpiry(leases, playerID, 1)
	if !found || !latest.Equal(late) {
		t.Fatal("live lease failed to win")
	}
	if _, found := domain.LatestEffectiveLeaseExpiry(nil, playerID, 1); found {
		t.Fatal("invented missing expiry")
	}
}
func TestAggregateDeepCopyIsolation(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	original := domain.Aggregate{Lobby: domain.Lobby{ID: roomID, GameKey: "quiz", HostPlayerID: pointer(playerID), NextJoinSequence: 2}, Teams: []domain.Team{{ID: "team", Name: "red"}}, Members: []domain.Member{{ID: "member", PlayerID: playerID, TeamID: pointer("team"), DisconnectedUntil: &now}}, Leases: []domain.Lease{{PlayerID: playerID, ReleasedAt: pointer(now)}}, StartRoster: domain.StartRoster{{PlayerID: playerID, TeamID: "team"}}}
	before, _ := json.Marshal(original)
	copied := original.DeepCopy()
	*copied.Lobby.HostPlayerID = "other"
	copied.Lobby.NextJoinSequence = 10
	copied.Teams[0].Name = "blue"
	*copied.Members[0].TeamID = "other"
	*copied.Members[0].DisconnectedUntil = now.Add(time.Hour)
	copied.Members[0].PlayerID = "other"
	*copied.Leases[0].ReleasedAt = now.Add(time.Hour)
	copied.StartRoster[0].TeamID = "other"
	after, _ := json.Marshal(original)
	if string(before) != string(after) {
		t.Fatal("copy mutated original")
	}
	empty := (domain.Aggregate{}).DeepCopy()
	if empty.Teams == nil || empty.Members == nil || empty.Leases == nil || empty.StartRoster == nil {
		t.Fatal("nil arrays")
	}
}
func TestMergeChanges(t *testing.T) {
	first := domain.RevocationIntent{PlayerID: playerID, Scope: domain.Scope{GameKey: "quiz", LobbyID: roomID}, Generation: 1}
	second := first
	second.Generation = 2
	result := domain.MergeChanges(domain.Change{PublicChanged: true, Revocations: []domain.RevocationIntent{first}}, domain.Change{PublicChanged: true, PrivateChanged: true, Revocations: []domain.RevocationIntent{first, second}})
	if !result.PublicChanged || !result.PrivateChanged || !reflect.DeepEqual(result.Revocations, []domain.RevocationIntent{first, second}) {
		t.Fatalf("merge %#v", result)
	}
	private := domain.MergeChanges(domain.Change{PrivateChanged: true})
	if private.PublicChanged || !private.HasWrites() {
		t.Fatal("private change became public or lost")
	}
	if (domain.Change{}).HasWrites() || !(domain.Change{Revocations: []domain.RevocationIntent{first}}).HasWrites() {
		t.Fatal("write classification")
	}
}
func TestBuildSnapshotFiltersOrdersAndCopies(t *testing.T) {
	aggregate := domain.Aggregate{Lobby: domain.Lobby{ID: roomID, GameKey: "quiz", HostPlayerID: pointer(playerID), GameSettings: json.RawMessage(`{"rounds":3}`)}, Teams: []domain.Team{{ID: "b", Order: 2}, {ID: "a", Order: 1}}, Members: []domain.Member{{ID: "b", PlayerID: playerID, Active: true, Generation: 2, JoinSequence: 2}, {ID: "old", PlayerID: "gone", Active: false}, {ID: "a", PlayerID: "other", Active: true, JoinSequence: 1}}}
	snapshot, err := domain.BuildSnapshot(aggregate, playerID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Members) != 2 || snapshot.Members[0].ID != "a" || snapshot.Teams[0].ID != "a" || snapshot.MembershipGeneration != 2 || snapshot.StartRoster == nil {
		t.Fatalf("snapshot %#v", snapshot)
	}
	*snapshot.Lobby.HostPlayerID = "changed"
	snapshot.Lobby.GameSettings[2] = 'R'
	if *aggregate.Lobby.HostPlayerID != playerID || string(aggregate.Lobby.GameSettings) != `{"rounds":3}` {
		t.Fatal("aliased snapshot")
	}
	if _, err := domain.BuildSnapshot(aggregate, "gone"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("inactive read allowed")
	}
}

func TestProductionIdentifiersAreCanonical(t *testing.T) {
	source := domain.CryptoIdentifiers{}
	id, err := source.UUID()
	if err != nil || domain.ValidateUUID(id) != nil || id[14] != '4' || !strings.ContainsRune("89ab", rune(id[19])) {
		t.Fatalf("invalid UUID %q: %v", id, err)
	}
	code, err := source.JoinCode()
	normalized, validation := domain.NormalizeJoinCode(code)
	if err != nil || validation != nil || normalized != code {
		t.Fatalf("invalid code %q: %v %v", code, err, validation)
	}
	random, err := source.Bytes(32)
	if err != nil || len(random) != 32 {
		t.Fatal("incorrect random length", err)
	}
	if _, err := source.Bytes(-1); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatal("negative count accepted")
	}
	if (domain.SystemClock{}).Now().Location() != time.UTC {
		t.Fatal("clock is not UTC")
	}
}
