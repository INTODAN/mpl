package lobby_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"islandBattles/lobby"
)

const roomID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
const playerID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"

func pointer[T any](value T) *T { return &value }
func TestChannelCanonicalRoundTrip(t *testing.T) {
	for _, game := range []string{"island-battles", "quiz-demo", "a", "a0-"} {
		scope := lobby.Scope{GameKey: game, LobbyID: roomID}
		channel, err := lobby.RoomChannel(scope)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := lobby.ParseChannel(channel)
		if err != nil || parsed.Namespace != lobby.ChannelLobby || parsed.Scope != scope {
			t.Fatalf("roundtrip: %#v %v", parsed, err)
		}
		rebuilt, err := lobby.RoomChannel(parsed.Scope)
		if err != nil || rebuilt != channel {
			t.Fatal("noncanonical roundtrip")
		}
	}
	channel, err := lobby.PlayerChannel(playerID)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := lobby.ParseChannel(channel)
	if err != nil || parsed.PlayerID != playerID || parsed.Namespace != lobby.ChannelPlayer {
		t.Fatalf("player: %#v %v", parsed, err)
	}
	rebuilt, err := lobby.PlayerChannel(parsed.PlayerID)
	if err != nil || rebuilt != channel {
		t.Fatal("noncanonical player roundtrip")
	}
}
func TestChannelRejectsAmbiguousIdentifiers(t *testing.T) {
	for _, value := range []string{"", ":" + playerID, "unknown:" + playerID, "player:", "player:" + playerID + ":x", "player:" + strings.ToUpper(playerID), "player:" + playerID + "#x", "player:$" + playerID, "player:/" + playerID, "player:%62" + playerID[1:], "player:123", "lobby:Quiz:" + roomID, "lobby:1quiz:" + roomID, "lobby:quiz_demo:" + roomID, "lobby::" + roomID, "lobby:" + strings.Repeat("a", 33) + ":" + roomID, "lobby:quiz:" + roomID + ":x", "lobby:quiz#x:" + roomID, "lobby:quiz/:" + roomID} {
		t.Run(value, func(t *testing.T) {
			if _, err := lobby.ParseChannel(value); !errors.Is(err, lobby.ErrInvalidInput) {
				t.Fatalf("accepted %q", value)
			}
		})
	}
}
func TestJoinCodeNormalization(t *testing.T) {
	normalized, err := lobby.NormalizeJoinCode(" \t\nabcd2345\r\v\f")
	if err != nil || normalized != "ABCD2345" {
		t.Fatalf("%q %v", normalized, err)
	}
	for _, value := range []string{"ABCD234", "ABCD23456", "ABCD2340", "ABCD2341", "ABCD234I", "ABCD234O", "ＡBCD2345", "\u00a0ABCD2345", "ABCD 345", "éBCD2345"} {
		if _, err := lobby.NormalizeJoinCode(value); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
}
func TestPolicyValidation(t *testing.T) {
	valid := lobby.DefaultPolicy("island-battles", "quiz-demo")
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	if valid.HeartbeatInterval != 15*time.Second || valid.LeaseDuration != 45*time.Second || valid.GraceDuration != 60*time.Second || valid.SweepInterval != 5*time.Second || valid.SessionDuration != 24*time.Hour || valid.ConnectionTokenDuration != 5*time.Minute {
		t.Fatal("incorrect defaults")
	}
	if err := (lobby.Policy{}).Validate(); err == nil {
		t.Fatal("zero policy accepted")
	}
	for _, mutate := range []func(*lobby.Policy){func(p *lobby.Policy) { p.RegisteredGameKeys = nil }, func(p *lobby.Policy) { p.RegisteredGameKeys = []string{"quiz", "quiz"} }, func(p *lobby.Policy) { p.RegisteredGameKeys = []string{"Quiz"} }, func(p *lobby.Policy) { p.HeartbeatInterval = 0 }, func(p *lobby.Policy) { p.LeaseDuration = p.HeartbeatInterval }, func(p *lobby.Policy) { p.GraceDuration = -time.Second }, func(p *lobby.Policy) { p.SweepInterval = p.GraceDuration + time.Second }, func(p *lobby.Policy) { p.SweepInterval = 0 }, func(p *lobby.Policy) { p.SessionDuration = 0 }, func(p *lobby.Policy) { p.ConnectionTokenDuration = p.SessionDuration + time.Second }, func(p *lobby.Policy) { p.SubscriptionDuration = 0 }} {
		invalid := valid
		mutate(&invalid)
		if err := invalid.Validate(); err == nil {
			t.Errorf("accepted invalid policy %#v", invalid)
		}
	}
	if valid.ValidateGame("quiz-demo") != nil || valid.ValidateGame("unregistered") == nil {
		t.Fatal("game registration ignored")
	}
}
func TestTeamDefinitionValidation(t *testing.T) {
	valid := []lobby.TeamDefinition{{Name: "  אדום ", Capacity: 2}, {Name: "青", Capacity: 2}}
	normalized, err := lobby.ValidateTeamDefinitions(valid, 4)
	if err != nil || normalized[0].Name != "אדום" || valid[0].Name != "  אדום " {
		t.Fatalf("labels mutated/lost: %#v %v", normalized, err)
	}
	for _, definitions := range [][]lobby.TeamDefinition{
		{}, {{Name: "a", Capacity: 2}}, make([]lobby.TeamDefinition, 17),
		{{Name: " red ", Capacity: 2}, {Name: "RED", Capacity: 2}},
		{{Name: "Straße", Capacity: 2}, {Name: "STRASSE", Capacity: 2}},
		{{Name: "a", Capacity: 0}, {Name: "b", Capacity: 4}},
		{{Name: "a", Capacity: 65}, {Name: "b", Capacity: 4}},
		{{Name: "a", Capacity: 1}, {Name: "b", Capacity: 1}},
		{{Name: " ", Capacity: 4}, {Name: "b", Capacity: 4}},
		{{Name: strings.Repeat("界", 41), Capacity: 4}, {Name: "b", Capacity: 4}},
	} {
		if _, err := lobby.ValidateTeamDefinitions(definitions, 4); err == nil {
			t.Fatalf("accepted %#v", definitions)
		}
	}
}
func TestHostElectionDeterministic(t *testing.T) {
	members := []lobby.Member{{ID: "0", PlayerID: "inactive", JoinSequence: 1}, {ID: "z", PlayerID: "late", JoinSequence: 3, Active: true}, {ID: "b", PlayerID: "second", JoinSequence: 2, Active: true}, {ID: "a", PlayerID: "first", JoinSequence: 2, Active: true}}
	host := lobby.ElectHost(members)
	if host == nil || *host != "first" {
		t.Fatalf("host %v", host)
	}
	*host = "mutated"
	if members[3].PlayerID != "first" {
		t.Fatal("host pointer aliases member")
	}
	if lobby.ElectHost(nil) != nil || lobby.ElectHost([]lobby.Member{{Active: false}}) != nil {
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
		if got := lobby.EffectiveLeaseExpiry(lobby.Lease{ExpiresAt: now, ReleasedAt: test.released}); !got.Equal(test.want) {
			t.Fatalf("got %v want %v", got, test.want)
		}
	}
	leases := []lobby.Lease{{PlayerID: playerID, Generation: 1, ExpiresAt: late, ReleasedAt: &early}, {PlayerID: playerID, Generation: 1, ExpiresAt: now, ReleasedAt: &late}, {PlayerID: playerID, Generation: 2, ExpiresAt: late}}
	latest, found := lobby.LatestEffectiveLeaseExpiry(leases, playerID, 1)
	if !found || !latest.Equal(now) {
		t.Fatal("released max wrong", latest)
	}
	if lobby.HasLiveLease(leases, playerID, 1, early) {
		t.Fatal("released lease is live")
	}
	if lobby.HasLiveLease(leases, playerID, 2, late) || !lobby.HasLiveLease(leases, playerID, 2, late.Add(-time.Nanosecond)) {
		t.Fatal("lease boundary is not strict")
	}
	leases = append(leases, lobby.Lease{PlayerID: playerID, Generation: 1, ExpiresAt: late})
	latest, found = lobby.LatestEffectiveLeaseExpiry(leases, playerID, 1)
	if !found || !latest.Equal(late) {
		t.Fatal("live lease failed to win")
	}
	if _, found := lobby.LatestEffectiveLeaseExpiry(nil, playerID, 1); found {
		t.Fatal("invented missing expiry")
	}
}
func TestAggregateDeepCopyIsolation(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	original := lobby.Aggregate{Lobby: lobby.Lobby{ID: roomID, GameKey: "quiz", HostPlayerID: pointer(playerID), NextJoinSequence: 2}, Teams: []lobby.Team{{ID: "team", Name: "red"}}, Members: []lobby.Member{{ID: "member", PlayerID: playerID, TeamID: pointer("team"), DisconnectedUntil: &now}}, Leases: []lobby.Lease{{PlayerID: playerID, ReleasedAt: pointer(now)}}, StartRoster: lobby.StartRoster{{PlayerID: playerID, TeamID: "team"}}}
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
	empty := (lobby.Aggregate{}).DeepCopy()
	if empty.Teams == nil || empty.Members == nil || empty.Leases == nil || empty.StartRoster == nil {
		t.Fatal("nil arrays")
	}
}
func TestMergeChanges(t *testing.T) {
	first := lobby.RevocationIntent{PlayerID: playerID, Scope: lobby.Scope{GameKey: "quiz", LobbyID: roomID}, Generation: 1}
	second := first
	second.Generation = 2
	result := lobby.MergeChanges(lobby.Change{PublicChanged: true, Revocations: []lobby.RevocationIntent{first}}, lobby.Change{PublicChanged: true, PrivateChanged: true, Revocations: []lobby.RevocationIntent{first, second}})
	if !result.PublicChanged || !result.PrivateChanged || !reflect.DeepEqual(result.Revocations, []lobby.RevocationIntent{first, second}) {
		t.Fatalf("merge %#v", result)
	}
	private := lobby.MergeChanges(lobby.Change{PrivateChanged: true})
	if private.PublicChanged || !private.HasWrites() {
		t.Fatal("private change became public or lost")
	}
	if (lobby.Change{}).HasWrites() || !(lobby.Change{Revocations: []lobby.RevocationIntent{first}}).HasWrites() {
		t.Fatal("write classification")
	}
}
func TestBuildSnapshotFiltersOrdersAndCopies(t *testing.T) {
	aggregate := lobby.Aggregate{Lobby: lobby.Lobby{ID: roomID, GameKey: "quiz", HostPlayerID: pointer(playerID)}, Teams: []lobby.Team{{ID: "b", Order: 2}, {ID: "a", Order: 1}}, Members: []lobby.Member{{ID: "b", PlayerID: playerID, Active: true, Generation: 2, JoinSequence: 2}, {ID: "old", PlayerID: "gone", Active: false}, {ID: "a", PlayerID: "other", Active: true, JoinSequence: 1}}}
	snapshot, err := lobby.BuildSnapshot(aggregate, playerID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Members) != 2 || snapshot.Members[0].ID != "a" || snapshot.Teams[0].ID != "a" || snapshot.MembershipGeneration != 2 || snapshot.StartRoster == nil {
		t.Fatalf("snapshot %#v", snapshot)
	}
	*snapshot.Lobby.HostPlayerID = "changed"
	if *aggregate.Lobby.HostPlayerID != playerID {
		t.Fatal("aliased snapshot")
	}
	if _, err := lobby.BuildSnapshot(aggregate, "gone"); !errors.Is(err, lobby.ErrNotFound) {
		t.Fatal("inactive read allowed")
	}
}

func TestProductionIdentifiersAreCanonical(t *testing.T) {
	source := lobby.CryptoIdentifiers{}
	id, err := source.UUID()
	if err != nil || lobby.ValidateUUID(id) != nil || id[14] != '4' || !strings.ContainsRune("89ab", rune(id[19])) {
		t.Fatalf("invalid UUID %q: %v", id, err)
	}
	code, err := source.JoinCode()
	normalized, validation := lobby.NormalizeJoinCode(code)
	if err != nil || validation != nil || normalized != code {
		t.Fatalf("invalid code %q: %v %v", code, err, validation)
	}
	random, err := source.Bytes(32)
	if err != nil || len(random) != 32 {
		t.Fatal("incorrect random length", err)
	}
	if _, err := source.Bytes(-1); !errors.Is(err, lobby.ErrInvalidInput) {
		t.Fatal("negative count accepted")
	}
	if (lobby.SystemClock{}).Now().Location() != time.UTC {
		t.Fatal("clock is not UTC")
	}
}
