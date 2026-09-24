package application_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"islandBattles/lobby/application"
	"islandBattles/lobby/domain"
	"islandBattles/lobby/postgres"
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
	jobs := fixture.jobs(t, "")
	fixture.clock.Set(fixture.origin.Add(46 * time.Second))
	result, err = fixture.service.Heartbeat(context.Background(), fixture.host, fixture.scope, domain.HeartbeatRequest{ClientInstanceID: fixture.clientID, MembershipGeneration: 1})
	if err != nil || result.Version != version || fixture.jobs(t, "") != jobs || !result.LeaseExpiresAt.Equal(fixture.origin.Add(91*time.Second)) {
		t.Fatal("lease-only heartbeat invalidated", err)
	}
}
func TestHeartbeatAtGraceBoundaryRequiresJoin(t *testing.T) {
	fixture := newServiceFixture(t, 2)
	fixture.clock.Set(fixture.origin.Add(105 * time.Second))
	_, err := fixture.service.Heartbeat(context.Background(), fixture.host, fixture.scope, domain.HeartbeatRequest{ClientInstanceID: fixture.clientID, MembershipGeneration: 1})
	requireDomainError(t, err, domain.ErrStaleMembership)
	aggregate := fixture.read(t)
	if aggregate.Members[0].Active || aggregate.Lobby.Phase != domain.PhaseClosed || aggregate.Lobby.Version != 2 || fixture.jobs(t, domain.JobRevocation) != 1 {
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
	freshStore, err := postgres.NewStore(fixture.db, fixture.clock, domain.CryptoIdentifiers{})
	if err != nil {
		t.Fatal(err)
	}
	sweeper, err := application.NewSweeper(freshStore, fixture.clock, fixture.policy, 10)
	if err != nil {
		t.Fatal(err)
	}
	fixture.clock.Set(fixture.origin.Add(time.Hour))
	if err := sweeper.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fixture.read(t).Members[0].Active {
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
func TestStartedExpiryPreservesRosterPostgres(t *testing.T) {
	fixture := newServiceFixture(t, 3)
	fixture.readyPair(t)
	started, err := fixture.service.Start(context.Background(), fixture.host, fixture.scope)
	if err != nil {
		t.Fatal(err)
	}
	fixture.clock.Set(fixture.origin.Add(time.Hour))
	sweeper, err := application.NewSweeper(fixture.store, fixture.clock, fixture.policy, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := sweeper.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	aggregate := fixture.read(t)
	if aggregate.Lobby.Phase != domain.PhaseStarted || aggregate.Lobby.HostPlayerID != nil || activeCount(aggregate) != 0 || !reflect.DeepEqual(aggregate.StartRoster, started.StartRoster) || fixture.jobs(t, domain.JobRevocation) != 2 {
		t.Fatal("started expiry changed game roster")
	}
	returned, _ := fixture.join(t, fixture.host)
	if returned.MembershipGeneration != 2 || !reflect.DeepEqual(returned.StartRoster, started.StartRoster) {
		t.Fatal("expired roster identity failed rejoin")
	}
}
func TestConcurrentHeartbeatAndSweepSerialize(t *testing.T) {
	for _, offset := range []time.Duration{105*time.Second - time.Microsecond, 105 * time.Second} {
		t.Run(offset.String(), func(t *testing.T) {
			fixture := newServiceFixture(t, 2)
			fixture.clock.Set(fixture.origin.Add(offset))
			sweeper, err := application.NewSweeper(fixture.store, fixture.clock, fixture.policy, 10)
			if err != nil {
				t.Fatal(err)
			}
			barrier := make(chan struct{})
			heartbeat := make(chan error, 1)
			sweep := make(chan error, 1)
			go func() {
				<-barrier
				_, err := fixture.service.Heartbeat(context.Background(), fixture.host, fixture.scope, domain.HeartbeatRequest{ClientInstanceID: fixture.clientID, MembershipGeneration: 1})
				heartbeat <- err
			}()
			go func() { <-barrier; sweep <- sweeper.RunOnce(context.Background()) }()
			close(barrier)
			heartbeatErr := <-heartbeat
			if err := <-sweep; err != nil {
				t.Fatal(err)
			}
			aggregate := fixture.read(t)
			if offset < 105*time.Second {
				if heartbeatErr != nil || !aggregate.Members[0].Active || aggregate.Members[0].DisconnectedUntil != nil || fixture.jobs(t, domain.JobRevocation) != 0 {
					t.Fatal("pre-deadline renewal and expiry both committed", heartbeatErr)
				}
			} else {
				requireDomainError(t, heartbeatErr, domain.ErrStaleMembership)
				if aggregate.Members[0].Active || fixture.jobs(t, domain.JobRevocation) != 1 || fixture.jobs(t, domain.JobInvalidation) != 2 {
					t.Fatal("deadline cleanup duplicated or membership revived")
				}
			}
		})
	}
}
func TestTwoSweepersDoNotDuplicateExpiry(t *testing.T) {
	fixture := newServiceFixture(t, 2)
	fixture.clock.Set(fixture.origin.Add(105 * time.Second))
	other, err := postgres.NewStore(fixture.db, fixture.clock, domain.CryptoIdentifiers{})
	if err != nil {
		t.Fatal(err)
	}
	barrier := make(chan struct{})
	results := make(chan error, 2)
	for _, store := range []domain.Store{fixture.store, other} {
		sweeper, err := application.NewSweeper(store, fixture.clock, fixture.policy, 10)
		if err != nil {
			t.Fatal(err)
		}
		go func() { <-barrier; results <- sweeper.RunOnce(context.Background()) }()
	}
	close(barrier)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if fixture.read(t).Lobby.Version != 2 || fixture.jobs(t, domain.JobInvalidation) != 2 || fixture.jobs(t, domain.JobRevocation) != 1 {
		t.Fatal("two sweepers duplicated expiry")
	}
}

type failingScopeStore struct {
	domain.Store
	scope domain.Scope
}

func (store failingScopeStore) Transact(ctx context.Context, scope domain.Scope, mutation domain.Mutation) (domain.Aggregate, error) {
	if scope == store.scope {
		return domain.Aggregate{}, domain.ErrUnavailable
	}
	return store.Store.Transact(ctx, scope, mutation)
}
func TestSweepCancellationAndPerRoomFailure(t *testing.T) {
	fixture := newServiceFixture(t, 2)
	request := fixture.request
	request.RequestID = integrationID(t)
	second, err := fixture.service.Create(context.Background(), fixture.host, request)
	if err != nil {
		t.Fatal(err)
	}
	fixture.clock.Set(fixture.origin.Add(time.Hour))
	sweeper, err := application.NewSweeper(failingScopeStore{fixture.store, fixture.scope}, fixture.clock, fixture.policy, 1)
	if err != nil {
		t.Fatal(err)
	}
	failures := 0
	for range 2 {
		err := sweeper.RunOnce(context.Background())
		if errors.Is(err, domain.ErrUnavailable) {
			failures++
		} else if err != nil {
			t.Fatal(err)
		}
	}
	unaffected, err := fixture.store.Read(context.Background(), second.Lobby.Scope())
	if err != nil || failures != 1 || unaffected.Lobby.Phase != domain.PhaseClosed || !fixture.read(t).Members[0].Active {
		t.Fatal("failed scope starved independent room", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sweeper.RunOnce(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation not propagated", err)
	}
	sweeper.Run(ctx, func(error) { t.Fatal("canceled run scheduled work") })
}
