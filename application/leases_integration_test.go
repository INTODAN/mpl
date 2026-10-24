package application_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"islandBattles/lobby/domain"
)

func TestHeartbeatDuringGracePreservesMembership(t *testing.T) {
	fixture := newServiceFixture(t, 2)
	fixture.assign(t, fixture.host, 0, true)
	before := memberFor(t, fixture.read(t), fixture.host.PlayerID)
	fixture.clock.Set(fixture.origin.Add(45 * time.Second))
	grace, err := fixture.service.Snapshot(context.Background(), fixture.host, fixture.scope)
	if err != nil {
		t.Fatal(err)
	}
	deadline := fixture.origin.Add(105 * time.Second)
	if grace.Members[0].DisconnectedUntil == nil || !grace.Members[0].DisconnectedUntil.Equal(deadline) {
		t.Fatal("grace deadline not durable")
	}
	result, err := fixture.service.Heartbeat(context.Background(), fixture.host, fixture.scope, domain.HeartbeatRequest{ClientInstanceID: fixture.clientID, MembershipGeneration: 1})
	if err != nil {
		t.Fatal(err)
	}
	aggregate := fixture.read(t)
	after := memberFor(t, aggregate, fixture.host.PlayerID)
	if before.ID != after.ID || after.Generation != 1 || !after.Ready || after.TeamID == nil || *after.TeamID != *before.TeamID || after.DisconnectedUntil != nil || *aggregate.Lobby.HostPlayerID != fixture.host.PlayerID || result.Version != aggregate.Lobby.Version || result.Version != grace.Lobby.Version+1 || !result.LeaseExpiresAt.Equal(fixture.origin.Add(90*time.Second)) {
		t.Fatal("grace heartbeat changed membership or returned old version", result, after)
	}
	version := aggregate.Lobby.Version
	pushes := fixture.pushed(allPushes)
	fixture.clock.Set(fixture.origin.Add(46 * time.Second))
	result, err = fixture.service.Heartbeat(context.Background(), fixture.host, fixture.scope, domain.HeartbeatRequest{ClientInstanceID: fixture.clientID, MembershipGeneration: 1})
	if err != nil || result.Version != version || fixture.pushed(allPushes) != pushes || !result.LeaseExpiresAt.Equal(fixture.origin.Add(91*time.Second)) {
		t.Fatal("lease-only heartbeat invalidated", err)
	}
}
func TestHeartbeatAtGraceBoundaryRequiresJoin(t *testing.T) {
	fixture := newServiceFixture(t, 2)
	fixture.clock.Set(fixture.origin.Add(105 * time.Second))
	_, err := fixture.service.Heartbeat(context.Background(), fixture.host, fixture.scope, domain.HeartbeatRequest{ClientInstanceID: fixture.clientID, MembershipGeneration: 1})
	requireDomainError(t, err, domain.ErrStaleMembership)
	aggregate := fixture.read(t)
	if aggregate.Members[0].Active || aggregate.Lobby.Phase != domain.PhaseClosed || aggregate.Lobby.Version != 2 || fixture.pushed(revocationPushes) != 1 {
		t.Fatal("boundary heartbeat revived membership or lost cleanup")
	}
}
func TestReleasedAndUnknownLeaseTuplePolicy(t *testing.T) {
	fixture := newServiceFixture(t, 2)
	request := domain.ReleaseLeaseRequest{ClientInstanceID: fixture.clientID, MembershipGeneration: 1}
	if err := fixture.service.ReleaseLease(context.Background(), fixture.host, fixture.scope, request); err != nil {
		t.Fatal(err)
	}
	first := fixture.read(t)
	if first.Members[0].DisconnectedUntil == nil || !first.Members[0].DisconnectedUntil.Equal(fixture.origin.Add(time.Minute)) {
		t.Fatal("release did not shorten deadline")
	}
	fixture.clock.Set(fixture.origin.Add(10 * time.Second))
	if err := fixture.service.ReleaseLease(context.Background(), fixture.host, fixture.scope, request); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, fixture.read(t)) {
		t.Fatal("repeat release extended grace")
	}
	for _, client := range []string{fixture.clientID, integrationID(t)} {
		_, err := fixture.service.Heartbeat(context.Background(), fixture.host, fixture.scope, domain.HeartbeatRequest{ClientInstanceID: client, MembershipGeneration: 1})
		requireDomainError(t, err, domain.ErrStaleMembership)
	}
	if err := fixture.service.ReleaseLease(context.Background(), fixture.host, fixture.scope, domain.ReleaseLeaseRequest{ClientInstanceID: integrationID(t), MembershipGeneration: 1}); err != nil {
		t.Fatal("unknown release not idempotent", err)
	}
	_, err := fixture.service.Join(context.Background(), fixture.host, domain.JoinRequest{GameKey: fixture.scope.GameKey, JoinCode: fixture.initial.Lobby.JoinCode, ClientInstanceID: fixture.clientID})
	requireDomainError(t, err, domain.ErrStaleMembership)
	outsider := fixture.player(t, "Outsider")
	requireDomainError(t, fixture.service.ReleaseLease(context.Background(), outsider, fixture.scope, request), domain.ErrNotFound)
	fixture.clock.Set(fixture.origin.Add(time.Minute))
	if err := fixture.service.ReleaseLease(context.Background(), fixture.host, fixture.scope, request); err != nil {
		t.Fatal("inactive released tuple rejected", err)
	}
	inactive := fixture.read(t)
	if inactive.Members[0].Active {
		t.Fatal("released membership retained after grace")
	}
	if err := fixture.service.ReleaseLease(context.Background(), fixture.host, fixture.scope, request); err != nil || !reflect.DeepEqual(inactive, fixture.read(t)) {
		t.Fatal("inactive repeated release not idempotent", err)
	}
}
func TestReleaseAfterNaturalExpiryKeepsOriginalDeadlinePostgres(t *testing.T) {
	fixture := newServiceFixture(t, 2)
	fixture.clock.Set(fixture.origin.Add(80 * time.Second))
	if err := fixture.service.ReleaseLease(context.Background(), fixture.host, fixture.scope, domain.ReleaseLeaseRequest{ClientInstanceID: fixture.clientID, MembershipGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	aggregate := fixture.read(t)
	deadline := fixture.origin.Add(105 * time.Second)
	if aggregate.Members[0].DisconnectedUntil == nil || !aggregate.Members[0].DisconnectedUntil.Equal(deadline) {
		t.Fatal("release after expiry extended grace")
	}
	// A restarted backend has only the persisted deadlines. The next proxy call
	// for the room applies them.
	fixture.clock.Set(fixture.origin.Add(time.Hour))
	requireDomainError(t, fixture.proxy(t, fixture.newStore(t))(fixture.host), domain.ErrPermissionDenied)
	if fixture.read(t).Members[0].Active || fixture.pushed(revocationPushes) != 1 {
		t.Fatal("restart granted fresh grace")
	}
}
func TestTwoClientsShareOneSeatPostgres(t *testing.T) {
	fixture := newServiceFixture(t, 2)
	_, secondClient := fixture.join(t, fixture.host)
	fixture.assign(t, fixture.host, 0, true)
	before := fixture.read(t)
	if err := fixture.service.ReleaseLease(context.Background(), fixture.host, fixture.scope, domain.ReleaseLeaseRequest{ClientInstanceID: fixture.clientID, MembershipGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	after := fixture.read(t)
	if len(after.Members) != 1 || len(after.Leases) != 2 || after.Lobby.Version != before.Lobby.Version || !reflect.DeepEqual(before.Members, after.Members) || !after.Members[0].Ready {
		t.Fatal("one client's release altered shared seat")
	}
	fixture.clock.Set(fixture.origin.Add(30 * time.Second))
	if _, err := fixture.service.Heartbeat(context.Background(), fixture.host, fixture.scope, domain.HeartbeatRequest{ClientInstanceID: secondClient, MembershipGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	fixture.clock.Set(fixture.origin.Add(60 * time.Second))
	snapshot, err := fixture.service.Snapshot(context.Background(), fixture.host, fixture.scope)
	if err != nil || len(snapshot.Members) != 1 || snapshot.Members[0].DisconnectedUntil != nil {
		t.Fatal("released client overrode live client", err)
	}
}
func TestGraceRetainsLobbyAndTeamCapacity(t *testing.T) {
	fixture := newServiceFixture(t, 2)
	guest, client := fixture.readyPair(t)
	outsider := fixture.player(t, "Outsider")
	fixture.clock.Set(fixture.origin.Add(80 * time.Second))
	if _, err := fixture.service.Heartbeat(context.Background(), guest, fixture.scope, domain.HeartbeatRequest{ClientInstanceID: client, MembershipGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	_, err := fixture.service.Join(context.Background(), outsider, domain.JoinRequest{GameKey: fixture.scope.GameKey, JoinCode: fixture.initial.Lobby.JoinCode, ClientInstanceID: integrationID(t)})
	requireDomainError(t, err, domain.ErrLobbyFull)
	_, err = fixture.service.SelectTeam(context.Background(), guest, fixture.scope, domain.SelectTeamRequest{TeamID: &fixture.initial.Teams[0].ID})
	requireDomainError(t, err, domain.ErrTeamFull)
	fixture.clock.Set(fixture.origin.Add(105 * time.Second))
	fixture.join(t, outsider)
	if _, err := fixture.service.SelectTeam(context.Background(), guest, fixture.scope, domain.SelectTeamRequest{TeamID: &fixture.initial.Teams[0].ID}); err != nil {
		t.Fatal("expired team slot retained", err)
	}
	aggregate := fixture.read(t)
	if activeCount(aggregate) != 2 || *aggregate.Lobby.HostPlayerID != guest.PlayerID {
		t.Fatal("expiry lost capacity/host transition")
	}
}

// Without a sweeper, a connected peer's heartbeats apply and push the expiry of
// a member who went quiet.
func TestPeerHeartbeatsPushLeaseAndGraceExpiry(t *testing.T) {
	fixture := newServiceFixture(t, 2)
	guest := fixture.player(t, "Guest")
	_, guestClient := fixture.join(t, guest)
	heartbeat := func(offset time.Duration) {
		t.Helper()
		fixture.clock.Set(fixture.origin.Add(offset))
		if _, err := fixture.service.Heartbeat(context.Background(), guest, fixture.scope, domain.HeartbeatRequest{ClientInstanceID: guestClient, MembershipGeneration: 1}); err != nil {
			t.Fatal(err)
		}
	}
	heartbeat(30 * time.Second)
	if fixture.pushed(invalidationPushes) != 2 {
		t.Fatal("lease renewal pushed an invalidation")
	}
	heartbeat(50 * time.Second)
	host := memberFor(t, fixture.read(t), fixture.host.PlayerID)
	if host.DisconnectedUntil == nil || !host.DisconnectedUntil.Equal(fixture.origin.Add(105*time.Second)) || fixture.pushed(invalidationPushes) != 3 {
		t.Fatal("host lease expiry was not applied and pushed")
	}
	heartbeat(90 * time.Second)
	if fixture.pushed(allPushes) != 3 {
		t.Fatal("grace without change pushed")
	}
	heartbeat(105 * time.Second)
	aggregate := fixture.read(t)
	if memberFor(t, aggregate, fixture.host.PlayerID).Active || *aggregate.Lobby.HostPlayerID != guest.PlayerID || fixture.pushed(invalidationPushes) != 4 || fixture.pushed(revocationPushes) != 1 {
		t.Fatal("host grace expiry was not applied and pushed")
	}
}

// A join that finds its own expired membership commits the expiry and the new
// membership together, so the player's subscription must not be revoked.
func TestRejoinAfterGraceDoesNotRevokeTheNewMembership(t *testing.T) {
	fixture := newServiceFixture(t, 2)
	guest := fixture.player(t, "Guest")
	_, guestClient := fixture.join(t, guest)
	for _, offset := range []time.Duration{30 * time.Second, 60 * time.Second, 90 * time.Second} {
		fixture.clock.Set(fixture.origin.Add(offset))
		if _, err := fixture.service.Heartbeat(context.Background(), guest, fixture.scope, domain.HeartbeatRequest{ClientInstanceID: guestClient, MembershipGeneration: 1}); err != nil {
			t.Fatal(err)
		}
	}
	fixture.clock.Set(fixture.origin.Add(105 * time.Second))
	fixture.join(t, fixture.host)
	host := memberFor(t, fixture.read(t), fixture.host.PlayerID)
	if !host.Active || host.Generation != 2 || fixture.pushed(revocationPushes) != 0 {
		t.Fatal("rejoin revoked the new membership", host, fixture.pushed(revocationPushes))
	}
}

// A push can wait between retries. If the expired player rejoins meanwhile,
// the late unsubscribe must not drop their new subscription.
func TestLateRevocationSkipsAPlayerWhoRejoined(t *testing.T) {
	fixture := newServiceFixture(t, 2)
	guest := fixture.player(t, "Guest")
	_, guestClient := fixture.join(t, guest)
	heartbeat := func(offset time.Duration) {
		t.Helper()
		fixture.clock.Set(fixture.origin.Add(offset))
		if _, err := fixture.service.Heartbeat(context.Background(), guest, fixture.scope, domain.HeartbeatRequest{ClientInstanceID: guestClient, MembershipGeneration: 1}); err != nil {
			t.Fatal(err)
		}
	}
	for _, offset := range []time.Duration{30 * time.Second, 60 * time.Second, 90 * time.Second} {
		heartbeat(offset)
	}
	fixture.pushes.beforeRevoke = func() { fixture.join(t, fixture.host) }
	// The guest's heartbeat expires the host, and its push is still pending
	// when the host joins again.
	heartbeat(105 * time.Second)
	host := memberFor(t, fixture.read(t), fixture.host.PlayerID)
	if !host.Active || host.Generation != 2 || fixture.pushed(revocationPushes) != 0 {
		t.Fatal("late revocation dropped the rejoined player", host, fixture.pushed(revocationPushes))
	}
}
func TestStartedExpiryPreservesRosterPostgres(t *testing.T) {
	fixture := newServiceFixture(t, 3)
	fixture.readyPair(t)
	started, err := fixture.service.Start(context.Background(), fixture.host, fixture.scope)
	if err != nil {
		t.Fatal(err)
	}
	// A late heartbeat is rejected but still commits the expiry it finds.
	fixture.clock.Set(fixture.origin.Add(time.Hour))
	_, err = fixture.service.Heartbeat(context.Background(), fixture.host, fixture.scope, domain.HeartbeatRequest{ClientInstanceID: fixture.clientID, MembershipGeneration: 1})
	requireDomainError(t, err, domain.ErrStaleMembership)
	aggregate := fixture.read(t)
	if aggregate.Lobby.Phase != domain.PhaseStarted || aggregate.Lobby.HostPlayerID != nil || activeCount(aggregate) != 0 || !reflect.DeepEqual(aggregate.StartRoster, started.StartRoster) || fixture.pushed(revocationPushes) != 2 {
		t.Fatal("started expiry changed game roster")
	}
	returned, _ := fixture.join(t, fixture.host)
	if returned.MembershipGeneration != 2 || !reflect.DeepEqual(returned.StartRoster, started.StartRoster) {
		t.Fatal("expired roster identity failed rejoin")
	}
}
func TestConcurrentHeartbeatAndProxyCallSerialize(t *testing.T) {
	for _, offset := range []time.Duration{105*time.Second - time.Microsecond, 105 * time.Second} {
		t.Run(offset.String(), func(t *testing.T) {
			fixture := newServiceFixture(t, 2)
			fixture.clock.Set(fixture.origin.Add(offset))
			proxy := fixture.proxy(t, fixture.newStore(t))
			barrier := make(chan struct{})
			heartbeat := make(chan error, 1)
			subscription := make(chan error, 1)
			go func() {
				<-barrier
				_, err := fixture.service.Heartbeat(context.Background(), fixture.host, fixture.scope, domain.HeartbeatRequest{ClientInstanceID: fixture.clientID, MembershipGeneration: 1})
				heartbeat <- err
			}()
			go func() { <-barrier; subscription <- proxy(fixture.host) }()
			close(barrier)
			heartbeatErr, subscriptionErr := <-heartbeat, <-subscription
			aggregate := fixture.read(t)
			if offset < 105*time.Second {
				if heartbeatErr != nil || subscriptionErr != nil || !aggregate.Members[0].Active || aggregate.Members[0].DisconnectedUntil != nil || fixture.pushed(revocationPushes) != 0 {
					t.Fatal("pre-deadline renewal and expiry both committed", heartbeatErr, subscriptionErr)
				}
			} else {
				requireDomainError(t, heartbeatErr, domain.ErrStaleMembership)
				requireDomainError(t, subscriptionErr, domain.ErrPermissionDenied)
				if aggregate.Members[0].Active || fixture.pushed(revocationPushes) != 1 || fixture.pushed(invalidationPushes) != 2 {
					t.Fatal("deadline cleanup duplicated or membership revived")
				}
			}
		})
	}
}
func TestTwoInstancesProxyCallsDoNotDuplicateExpiry(t *testing.T) {
	fixture := newServiceFixture(t, 2)
	fixture.clock.Set(fixture.origin.Add(105 * time.Second))
	barrier := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		proxy := fixture.proxy(t, fixture.newStore(t))
		go func() { <-barrier; results <- proxy(fixture.host) }()
	}
	close(barrier)
	for range 2 {
		requireDomainError(t, <-results, domain.ErrPermissionDenied)
	}
	if fixture.read(t).Lobby.Version != 2 || fixture.pushed(invalidationPushes) != 2 || fixture.pushed(revocationPushes) != 1 {
		t.Fatal("two instances duplicated expiry")
	}
}
