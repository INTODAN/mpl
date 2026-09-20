package lobby_test

import (
	"reflect"
	"testing"
	"time"

	"islandBattles/lobby"
)

func expiryFixture(now time.Time) lobby.Aggregate {
	host := "host"
	team := "team"
	return lobby.Aggregate{
		Lobby:   lobby.Lobby{ID: "room", GameKey: "island-battles", Phase: lobby.PhaseWaiting, HostPlayerID: &host, Version: 5},
		Members: []lobby.Member{{ID: "member", PlayerID: host, Generation: 1, Active: true, TeamID: &team, Ready: true, JoinSequence: 1}},
		Leases:  []lobby.Lease{{LobbyID: "room", PlayerID: host, Generation: 1, ClientInstanceID: "client", ExpiresAt: now}},
	}
}

func TestExpiryGraceBoundaryAndRestart(t *testing.T) {
	now := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	policy := lobby.DefaultPolicy("island-battles")
	for _, test := range []struct {
		name   string
		offset time.Duration
		active bool
		grace  bool
	}{
		{"before lease expiry", -time.Nanosecond, true, false},
		{"lease expiry", 0, true, true},
		{"before grace expiry", policy.GraceDuration - time.Nanosecond, true, true},
		{"grace expiry", policy.GraceDuration, false, false},
		{"restart overdue", time.Hour, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			aggregate := expiryFixture(now)
			change, err := lobby.NormalizeExpired(&aggregate, now.Add(test.offset), policy)
			if err != nil {
				t.Fatal(err)
			}
			member := aggregate.Members[0]
			if member.Active != test.active || (member.DisconnectedUntil != nil) != test.grace {
				t.Fatalf("unexpected state: %+v", member)
			}
			if test.grace && !member.DisconnectedUntil.Equal(now.Add(policy.GraceDuration)) {
				t.Fatal("grace extended from sweep time")
			}
			if test.active && (!member.Ready || member.TeamID == nil || aggregate.Lobby.HostPlayerID == nil) {
				t.Fatal("grace discarded seat state")
			}
			if !test.active && (member.Ready || member.TeamID != nil || aggregate.Lobby.Phase != lobby.PhaseClosed || aggregate.Lobby.HostPlayerID != nil || len(change.Revocations) != 1) {
				t.Fatalf("incomplete expiry: %+v / %+v", aggregate, change)
			}
			second, err := lobby.NormalizeExpired(&aggregate, now.Add(test.offset), policy)
			if err != nil || second.HasWrites() {
				t.Fatalf("normalization is not idempotent: %+v %v", second, err)
			}
		})
	}
}

func TestTwoClientsShareOneSeat(t *testing.T) {
	now := time.Now().UTC()
	aggregate := expiryFixture(now.Add(-time.Hour))
	aggregate.Leases = append(aggregate.Leases, lobby.Lease{PlayerID: "host", Generation: 1, ClientInstanceID: "second", ExpiresAt: now.Add(time.Minute)})
	change, err := lobby.NormalizeExpired(&aggregate, now, lobby.DefaultPolicy("island-battles"))
	if err != nil || change.HasWrites() || !aggregate.Members[0].Active || len(aggregate.Members) != 1 {
		t.Fatalf("other lease lost membership: %+v %v", change, err)
	}
}

func TestReleaseAfterExpiryDoesNotExtendGrace(t *testing.T) {
	now := time.Now().UTC()
	aggregate := expiryFixture(now.Add(-time.Hour))
	aggregate.Leases[0].ReleasedAt = &now
	if _, err := lobby.NormalizeExpired(&aggregate, now, lobby.DefaultPolicy("island-battles")); err != nil {
		t.Fatal(err)
	}
	if aggregate.Members[0].Active {
		t.Fatal("late release extended expired lease")
	}
}

func TestAllReleasedLeasesHaveDurableDeadline(t *testing.T) {
	now := time.Now().UTC()
	aggregate := expiryFixture(now.Add(time.Hour))
	aggregate.Leases[0].ReleasedAt = &now
	policy := lobby.DefaultPolicy("island-battles")
	if _, err := lobby.NormalizeExpired(&aggregate, now, policy); err != nil {
		t.Fatal(err)
	}
	if aggregate.Members[0].DisconnectedUntil == nil || !aggregate.Members[0].DisconnectedUntil.Equal(now.Add(policy.GraceDuration)) {
		t.Fatal("released member has no correct deadline")
	}
	if _, err := lobby.NormalizeExpired(&aggregate, now.Add(policy.GraceDuration), policy); err != nil {
		t.Fatal(err)
	}
	if aggregate.Members[0].Active {
		t.Fatal("released member survived deadline")
	}
}

func TestOldGenerationLeaseCannotRetainMembership(t *testing.T) {
	now := time.Now().UTC()
	aggregate := expiryFixture(now.Add(-time.Hour))
	aggregate.Members[0].Generation = 2
	aggregate.Leases[0].Generation = 2
	aggregate.Leases = append(aggregate.Leases, lobby.Lease{PlayerID: "host", Generation: 1, ExpiresAt: now.Add(time.Hour)})
	if _, err := lobby.NormalizeExpired(&aggregate, now, lobby.DefaultPolicy("island-battles")); err != nil {
		t.Fatal(err)
	}
	if aggregate.Members[0].Active {
		t.Fatal("obsolete generation retained seat")
	}
}

func TestStartedExpiryPreservesRoster(t *testing.T) {
	now := time.Now().UTC()
	aggregate := expiryFixture(now.Add(-time.Hour))
	aggregate.Lobby.Phase = lobby.PhaseStarted
	aggregate.StartRoster = lobby.StartRoster{{PlayerID: "host", TeamID: "team", MemberID: "member", TeamOrder: 1}}
	original := append(lobby.StartRoster{}, aggregate.StartRoster...)
	if _, err := lobby.NormalizeExpired(&aggregate, now, lobby.DefaultPolicy("island-battles")); err != nil {
		t.Fatal(err)
	}
	if aggregate.Lobby.Phase != lobby.PhaseStarted || !reflect.DeepEqual(original, aggregate.StartRoster) || aggregate.Lobby.HostPlayerID != nil {
		t.Fatal("started roster/phase mutated")
	}
}

func TestExpiryElectsHostDeterministically(t *testing.T) {
	now := time.Now().UTC()
	aggregate := expiryFixture(now.Add(-time.Hour))
	for index, player := range []string{"later", "earlier"} {
		aggregate.Members = append(aggregate.Members, lobby.Member{ID: player, PlayerID: player, Active: true, Generation: 1, JoinSequence: int64(3 - index)})
		aggregate.Leases = append(aggregate.Leases, lobby.Lease{PlayerID: player, Generation: 1, ExpiresAt: now.Add(time.Minute)})
	}
	if _, err := lobby.NormalizeExpired(&aggregate, now, lobby.DefaultPolicy("island-battles")); err != nil {
		t.Fatal(err)
	}
	if aggregate.Lobby.HostPlayerID == nil || *aggregate.Lobby.HostPlayerID != "earlier" {
		t.Fatal("incorrect successor")
	}
}
