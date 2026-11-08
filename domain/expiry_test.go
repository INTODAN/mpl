package domain_test

import (
	"reflect"
	"testing"
	"time"

	"islandBattles/lobby/domain"
)

func expiryFixture(now time.Time) domain.Aggregate {
	host := "host"
	team := "team"
	return domain.Aggregate{
		Lobby:   domain.Lobby{ID: "room", GameKey: "island-battles", Phase: domain.PhaseWaiting, HostPlayerID: &host, Version: 5},
		Members: []domain.Member{{ID: "member", PlayerID: host, Generation: 1, Active: true, TeamID: &team, Ready: true, JoinSequence: 1}},
		Leases:  []domain.Lease{{LobbyID: "room", PlayerID: host, Generation: 1, ClientInstanceID: "client", ExpiresAt: now}},
	}
}

func TestExpiryGraceBoundaryAndRestart(t *testing.T) {
	now := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	policy := domain.DefaultPolicy(domain.GameRegistration{Key: "island-battles"})
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
			change, err := domain.NormalizeExpired(&aggregate, now.Add(test.offset), policy)
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
			if !test.active && (member.Ready || member.TeamID != nil || aggregate.Lobby.Phase != domain.PhaseClosed || aggregate.Lobby.HostPlayerID != nil || len(change.Revocations) != 1) {
				t.Fatalf("incomplete expiry: %+v / %+v", aggregate, change)
			}
			second, err := domain.NormalizeExpired(&aggregate, now.Add(test.offset), policy)
			if err != nil || second.HasWrites() {
				t.Fatalf("normalization is not idempotent: %+v %v", second, err)
			}
		})
	}
}

func TestTwoClientsShareOneSeat(t *testing.T) {
	now := time.Now().UTC()
	aggregate := expiryFixture(now.Add(-time.Hour))
	aggregate.Leases = append(aggregate.Leases, domain.Lease{PlayerID: "host", Generation: 1, ClientInstanceID: "second", ExpiresAt: now.Add(time.Minute)})
	change, err := domain.NormalizeExpired(&aggregate, now, domain.DefaultPolicy(domain.GameRegistration{Key: "island-battles"}))
	if err != nil || change.HasWrites() || !aggregate.Members[0].Active || len(aggregate.Members) != 1 {
		t.Fatalf("other lease lost membership: %+v %v", change, err)
	}
}

func TestReleaseAfterExpiryDoesNotExtendGrace(t *testing.T) {
	now := time.Now().UTC()
	aggregate := expiryFixture(now.Add(-time.Hour))
	aggregate.Leases[0].ReleasedAt = &now
	if _, err := domain.NormalizeExpired(&aggregate, now, domain.DefaultPolicy(domain.GameRegistration{Key: "island-battles"})); err != nil {
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
	policy := domain.DefaultPolicy(domain.GameRegistration{Key: "island-battles"})
	if _, err := domain.NormalizeExpired(&aggregate, now, policy); err != nil {
		t.Fatal(err)
	}
	if aggregate.Members[0].DisconnectedUntil == nil || !aggregate.Members[0].DisconnectedUntil.Equal(now.Add(policy.GraceDuration)) {
		t.Fatal("released member has no correct deadline")
	}
	if _, err := domain.NormalizeExpired(&aggregate, now.Add(policy.GraceDuration), policy); err != nil {
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
	aggregate.Leases = append(aggregate.Leases, domain.Lease{PlayerID: "host", Generation: 1, ExpiresAt: now.Add(time.Hour)})
	if _, err := domain.NormalizeExpired(&aggregate, now, domain.DefaultPolicy(domain.GameRegistration{Key: "island-battles"})); err != nil {
		t.Fatal(err)
	}
	if aggregate.Members[0].Active {
		t.Fatal("obsolete generation retained seat")
	}
}

func TestStartedExpiryPreservesRoster(t *testing.T) {
	now := time.Now().UTC()
	aggregate := expiryFixture(now.Add(-time.Hour))
	aggregate.Lobby.Phase = domain.PhaseStarted
	aggregate.StartRoster = domain.StartRoster{{PlayerID: "host", TeamID: "team", MemberID: "member", TeamOrder: 1}}
	original := append(domain.StartRoster{}, aggregate.StartRoster...)
	if _, err := domain.NormalizeExpired(&aggregate, now, domain.DefaultPolicy(domain.GameRegistration{Key: "island-battles"})); err != nil {
		t.Fatal(err)
	}
	if aggregate.Lobby.Phase != domain.PhaseStarted || !reflect.DeepEqual(original, aggregate.StartRoster) || aggregate.Lobby.HostPlayerID != nil {
		t.Fatal("started roster/phase mutated")
	}
}

func TestExpiryElectsHostDeterministically(t *testing.T) {
	now := time.Now().UTC()
	aggregate := expiryFixture(now.Add(-time.Hour))
	for index, player := range []string{"later", "earlier"} {
		aggregate.Members = append(aggregate.Members, domain.Member{ID: player, PlayerID: player, Active: true, Generation: 1, JoinSequence: int64(3 - index)})
		aggregate.Leases = append(aggregate.Leases, domain.Lease{PlayerID: player, Generation: 1, ExpiresAt: now.Add(time.Minute)})
	}
	if _, err := domain.NormalizeExpired(&aggregate, now, domain.DefaultPolicy(domain.GameRegistration{Key: "island-battles"})); err != nil {
		t.Fatal(err)
	}
	if aggregate.Lobby.HostPlayerID == nil || *aggregate.Lobby.HostPlayerID != "earlier" {
		t.Fatal("incorrect successor")
	}
}

func TestSeatsNeverExpireOrHost(t *testing.T) {
	now := time.Now().UTC()
	aggregate := expiryFixture(now.Add(-time.Hour))
	team := "team"
	aggregate.Members = append(aggregate.Members, domain.Member{ID: "seat", PlayerID: "seat-player", Active: true, Generation: 1, TeamID: &team, Ready: true, JoinSequence: 0, Kind: domain.MemberSeat, SeatSettings: []byte(`{"level":1}`)})
	if _, err := domain.NormalizeExpired(&aggregate, now, domain.DefaultPolicy(domain.GameRegistration{Key: "island-battles"})); err != nil {
		t.Fatal(err)
	}
	seat := domain.FindMember(&aggregate, "seat-player")
	if !seat.Active || !seat.Ready || seat.DisconnectedUntil != nil {
		t.Fatalf("seat after expiry = %#v", seat)
	}
	// The host's grace ran out; the seat, which joined earlier, doesn't host,
	// so the waiting lobby closes.
	if aggregate.Lobby.HostPlayerID != nil || aggregate.Lobby.Phase != domain.PhaseClosed {
		t.Fatalf("host %v, phase %s", aggregate.Lobby.HostPlayerID, aggregate.Lobby.Phase)
	}
}
