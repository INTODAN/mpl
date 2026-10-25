package application_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"islandBattles/lobby/application"
	"islandBattles/lobby/domain"
)

func TestSelectLastTeamSeatConcurrent(t *testing.T) {
	fixture := newServiceFixture(t, 3)
	guest := fixture.player(t, "Guest")
	fixture.join(t, guest)
	fixture.assign(t, fixture.host, 1, true)
	fixture.assign(t, guest, 1, true)
	before := fixture.read(t)
	barrier := make(chan struct{})
	type outcome struct {
		player string
		err    error
	}
	results := make(chan outcome, 2)
	target := fixture.initial.Teams[0].ID
	for _, actor := range []domain.Principal{fixture.host, guest} {
		go func() {
			<-barrier
			_, err := fixture.service.SelectTeam(context.Background(), actor, fixture.scope, domain.SelectTeamRequest{TeamID: &target})
			results <- outcome{actor.PlayerID, err}
		}()
	}
	close(barrier)
	winners, losers := 0, 0
	outcomes := []outcome{<-results, <-results}
	aggregate := fixture.read(t)
	for _, result := range outcomes {
		member := memberFor(t, aggregate, result.player)
		if result.err == nil {
			winners++
			if member.TeamID == nil || *member.TeamID != target || member.Ready {
				t.Fatal("winner not moved/unreadied")
			}
		} else {
			requireDomainError(t, result.err, domain.ErrTeamFull)
			losers++
			if member.TeamID == nil || *member.TeamID != fixture.initial.Teams[1].ID || !member.Ready {
				t.Fatal("loser lost previous selection/readiness")
			}
		}
	}
	if winners != 1 || losers != 1 || aggregate.Lobby.Version != before.Lobby.Version+1 {
		t.Fatal("team capacity race", aggregate)
	}
}
func TestTeamSelectionRejectsOtherRoomTeam(t *testing.T) {
	fixture := newServiceFixture(t, 3)
	fixture.assign(t, fixture.host, 0, true)
	before := fixture.read(t)
	request := fixture.request
	request.RequestID = integrationID(t)
	other, err := fixture.service.Create(context.Background(), fixture.host, request)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.service.SelectTeam(context.Background(), fixture.host, fixture.scope, domain.SelectTeamRequest{TeamID: &other.Teams[0].ID})
	requireDomainError(t, err, domain.ErrInvalidInput)
	outsider := fixture.player(t, "Outsider")
	_, err = fixture.service.SelectTeam(context.Background(), outsider, fixture.scope, domain.SelectTeamRequest{TeamID: &fixture.initial.Teams[0].ID})
	requireDomainError(t, err, domain.ErrNotFound)
	if !reflect.DeepEqual(before, fixture.read(t)) {
		t.Fatal("invalid team selection mutated aggregate")
	}
}
func TestTeamChangeClearsReadyAndNoOpsPreserveVersion(t *testing.T) {
	fixture := newServiceFixture(t, 3)
	fixture.assign(t, fixture.host, 0, true)
	before := fixture.read(t)
	pushes := fixture.pushed(allPushes)
	if _, err := fixture.service.SelectTeam(context.Background(), fixture.host, fixture.scope, domain.SelectTeamRequest{TeamID: &fixture.initial.Teams[0].ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.SetReady(context.Background(), fixture.host, fixture.scope, domain.ReadyRequest{Ready: true}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, fixture.read(t)) || fixture.pushed(allPushes) != pushes {
		t.Fatal("same desired state emitted event")
	}
	if _, err := fixture.service.SelectTeam(context.Background(), fixture.host, fixture.scope, domain.SelectTeamRequest{TeamID: &fixture.initial.Teams[1].ID}); err != nil {
		t.Fatal(err)
	}
	current := fixture.read(t)
	if current.Members[0].Ready || current.Members[0].TeamID == nil || *current.Members[0].TeamID != fixture.initial.Teams[1].ID {
		t.Fatal("switch failed to clear ready")
	}
	if _, err := fixture.service.SetReady(context.Background(), fixture.host, fixture.scope, domain.ReadyRequest{Ready: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.SelectTeam(context.Background(), fixture.host, fixture.scope, domain.SelectTeamRequest{TeamID: nil}); err != nil {
		t.Fatal(err)
	}
	current = fixture.read(t)
	if current.Members[0].Ready || current.Members[0].TeamID != nil {
		t.Fatal("null team/false ready not persisted")
	}
	before = current
	if _, err := fixture.service.SelectTeam(context.Background(), fixture.host, fixture.scope, domain.SelectTeamRequest{}); err != nil {
		t.Fatal(err)
	}
	if fixture.read(t).Lobby.Version != before.Lobby.Version {
		t.Fatal("repeat clear not no-op")
	}
}
func TestReadyValidation(t *testing.T) {
	fixture := newServiceFixture(t, 2)
	_, err := fixture.service.SetReady(context.Background(), fixture.host, fixture.scope, domain.ReadyRequest{Ready: true})
	requireDomainError(t, err, domain.ErrNotReady)
	snapshot, err := fixture.service.SetReady(context.Background(), fixture.host, fixture.scope, domain.ReadyRequest{Ready: false})
	if err != nil || snapshot.Lobby.Version != 1 || fixture.pushed(allPushes) != 1 {
		t.Fatal("false without assignment rejected or invalidated", err)
	}
}
func TestStartRequiresHostAndLiveReadyRoster(t *testing.T) {
	fixture := newServiceFixture(t, 3)
	_, err := fixture.service.Start(context.Background(), fixture.host, fixture.scope)
	requireDomainError(t, err, domain.ErrNotReady)
	guest := fixture.player(t, "Guest")
	_, client := fixture.join(t, guest)
	_, err = fixture.service.Start(context.Background(), guest, fixture.scope)
	requireDomainError(t, err, domain.ErrNotHost)
	fixture.assign(t, fixture.host, 1, true)
	_, err = fixture.service.Start(context.Background(), fixture.host, fixture.scope)
	requireDomainError(t, err, domain.ErrNotReady)
	fixture.assign(t, guest, 1, false)
	_, err = fixture.service.Start(context.Background(), fixture.host, fixture.scope)
	requireDomainError(t, err, domain.ErrNotReady)
	if _, err := fixture.service.SetReady(context.Background(), guest, fixture.scope, domain.ReadyRequest{Ready: true}); err != nil {
		t.Fatal(err)
	}
	fixture.clock.Set(fixture.origin.Add(45 * time.Second))
	_, err = fixture.service.Start(context.Background(), fixture.host, fixture.scope)
	requireDomainError(t, err, domain.ErrNotReady)
	for _, actor := range []struct {
		principal domain.Principal
		client    string
	}{{fixture.host, fixture.clientID}, {guest, client}} {
		if _, err := fixture.service.Heartbeat(context.Background(), actor.principal, fixture.scope, domain.HeartbeatRequest{ClientInstanceID: actor.client, MembershipGeneration: 1}); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := fixture.service.Start(context.Background(), fixture.host, fixture.scope)
	if err != nil || snapshot.Lobby.Phase != domain.PhaseStarted || len(snapshot.StartRoster) != 2 {
		t.Fatal("ready roster failed", err)
	}
	for _, entry := range snapshot.StartRoster {
		if entry.TeamOrder != 2 {
			t.Fatal("empty configured team changed stable order")
		}
	}
	before := fixture.read(t)
	pushes := fixture.pushed(allPushes)
	repeat, err := fixture.service.Start(context.Background(), fixture.host, fixture.scope)
	if err != nil || repeat.Lobby.Version != before.Lobby.Version || fixture.pushed(allPushes) != pushes || !reflect.DeepEqual(before.StartRoster, repeat.StartRoster) {
		t.Fatal("repeat start mutated roster/event", err)
	}
	_, err = fixture.service.SelectTeam(context.Background(), guest, fixture.scope, domain.SelectTeamRequest{})
	requireDomainError(t, err, domain.ErrPhaseConflict)
	_, err = fixture.service.SetReady(context.Background(), guest, fixture.scope, domain.ReadyRequest{Ready: false})
	requireDomainError(t, err, domain.ErrPhaseConflict)
}

func TestDefaultStartRuleAllowsEmptyTeam(t *testing.T) {
	fixture := newServiceFixture(t, 3, func(request *domain.CreateRequest) {
		request.GameKey = "quiz-demo"
	})
	if fixture.initial.StartRules.EveryTeamNeedsMember {
		t.Fatal("quiz-demo snapshot declares a start rule it lacks")
	}
	guest := fixture.player(t, "Guest")
	fixture.join(t, guest)
	fixture.assign(t, fixture.host, 1, true)
	fixture.assign(t, guest, 1, true)
	snapshot, err := fixture.service.Start(context.Background(), fixture.host, fixture.scope)
	if err != nil || snapshot.Lobby.Phase != domain.PhaseStarted || len(snapshot.StartRoster) != 2 {
		t.Fatal("quiz-demo with an empty team did not start", err)
	}
}

func TestStartRuleRequiresEveryTeamFilled(t *testing.T) {
	fixture := newServiceFixture(t, 3, func(request *domain.CreateRequest) {
		request.GameKey = "team-game"
		request.Teams = append(request.Teams, domain.TeamDefinition{Name: "Green", Capacity: 1})
	})
	if !fixture.initial.StartRules.EveryTeamNeedsMember {
		t.Fatal("snapshot lacks the game's start rule")
	}
	guest := fixture.player(t, "Guest")
	fixture.join(t, guest)
	fixture.assign(t, fixture.host, 1, true)
	fixture.assign(t, guest, 1, true)
	before := fixture.read(t)
	_, err := fixture.service.Start(context.Background(), fixture.host, fixture.scope)
	requireDomainError(t, err, domain.NewError(domain.CodeEmptyTeams, ""))
	var domainError *domain.Error
	if !errors.As(err, &domainError) || !reflect.DeepEqual(domainError.TeamIDs, []string{fixture.initial.Teams[0].ID, fixture.initial.Teams[2].ID}) || domainError.Message != "every team needs a player before the lobby can start; empty: Red, Green" {
		t.Fatalf("empty teams not named: %#v", domainError)
	}
	if after := fixture.read(t); after.Lobby.Phase != domain.PhaseWaiting || after.Lobby.Version != before.Lobby.Version {
		t.Fatal("refused start changed the lobby")
	}
	third := fixture.player(t, "Third")
	fixture.join(t, third)
	fixture.assign(t, guest, 0, true)
	fixture.assign(t, third, 2, true)
	snapshot, err := fixture.service.Start(context.Background(), fixture.host, fixture.scope)
	if err != nil || snapshot.Lobby.Phase != domain.PhaseStarted || len(snapshot.StartRoster) != 3 || !snapshot.StartRules.EveryTeamNeedsMember {
		t.Fatal("filled teams did not start", err)
	}
}
func TestStartAndTeamChangeSerialize(t *testing.T) {
	fixture := newServiceFixture(t, 3)
	guest, _ := fixture.readyPair(t)
	barrier := make(chan struct{})
	started := make(chan error, 1)
	changed := make(chan error, 1)
	go func() {
		<-barrier
		_, err := fixture.service.Start(context.Background(), fixture.host, fixture.scope)
		started <- err
	}()
	go func() {
		<-barrier
		_, err := fixture.service.SelectTeam(context.Background(), guest, fixture.scope, domain.SelectTeamRequest{})
		changed <- err
	}()
	close(barrier)
	startErr, changeErr := <-started, <-changed
	aggregate := fixture.read(t)
	if startErr == nil {
		requireDomainError(t, changeErr, domain.ErrPhaseConflict)
		if aggregate.Lobby.Phase != domain.PhaseStarted || len(aggregate.StartRoster) != 2 || memberFor(t, aggregate, guest.PlayerID).TeamID == nil {
			t.Fatal("inconsistent started roster")
		}
	} else {
		requireDomainError(t, startErr, domain.ErrNotReady)
		if changeErr != nil || aggregate.Lobby.Phase != domain.PhaseWaiting || len(aggregate.StartRoster) != 0 || memberFor(t, aggregate, guest.PlayerID).TeamID != nil {
			t.Fatal("invalid team-first ordering", changeErr)
		}
	}
}
func TestHostLeaveTransfersDeterministically(t *testing.T) {
	fixture := newServiceFixture(t, 3)
	first := fixture.player(t, "First")
	second := fixture.player(t, "Second")
	fixture.join(t, first)
	fixture.join(t, second)
	if err := fixture.service.Leave(context.Background(), fixture.host, fixture.scope); err != nil {
		t.Fatal(err)
	}
	aggregate := fixture.read(t)
	if aggregate.Lobby.HostPlayerID == nil || *aggregate.Lobby.HostPlayerID != first.PlayerID {
		t.Fatal("wrong successor")
	}
	before := aggregate
	events := fixture.pushed(allPushes)
	if err := fixture.service.Leave(context.Background(), fixture.host, fixture.scope); err != nil {
		t.Fatal(err)
	}
	if fixture.pushed(revocationPushes) != 1 || fixture.pushed(allPushes) != events || !reflect.DeepEqual(before, fixture.read(t)) {
		t.Fatal("repeated leave changed state/events")
	}
	outsider := fixture.player(t, "Outsider")
	requireDomainError(t, fixture.service.Leave(context.Background(), outsider, fixture.scope), domain.ErrNotFound)
}
func TestLastWaitingMemberLeaveClosesRoom(t *testing.T) {
	fixture := newServiceFixture(t, 2)
	if err := fixture.service.Leave(context.Background(), fixture.host, fixture.scope); err != nil {
		t.Fatal(err)
	}
	aggregate := fixture.read(t)
	if aggregate.Lobby.Phase != domain.PhaseClosed || aggregate.Lobby.HostPlayerID != nil {
		t.Fatal("last leave did not close")
	}
	_, err := fixture.service.Join(context.Background(), fixture.host, domain.JoinRequest{GameKey: fixture.scope.GameKey, JoinCode: fixture.initial.Lobby.JoinCode, ClientInstanceID: integrationID(t)})
	requireDomainError(t, err, domain.ErrNotFound)
}
func TestStartedRejoinRestoresFrozenTeam(t *testing.T) {
	fixture := newServiceFixture(t, 3)
	guest, _ := fixture.readyPair(t)
	started, err := fixture.service.Start(context.Background(), fixture.host, fixture.scope)
	if err != nil {
		t.Fatal(err)
	}
	hostBefore := memberFor(t, fixture.read(t), fixture.host.PlayerID)
	for _, actor := range []domain.Principal{fixture.host, guest} {
		if err := fixture.service.Leave(context.Background(), actor, fixture.scope); err != nil {
			t.Fatal(err)
		}
	}
	departed := fixture.read(t)
	if departed.Lobby.Phase != domain.PhaseStarted || departed.Lobby.HostPlayerID != nil || !reflect.DeepEqual(departed.StartRoster, started.StartRoster) || activeCount(departed) != 0 {
		t.Fatal("started leave rewrote frozen state")
	}
	returned, _ := fixture.join(t, fixture.host)
	hostAfter := memberFor(t, fixture.read(t), fixture.host.PlayerID)
	if hostAfter.ID != hostBefore.ID || hostAfter.Generation != 2 || hostAfter.TeamID == nil || *hostAfter.TeamID != *hostBefore.TeamID || hostAfter.Ready || returned.Lobby.HostPlayerID == nil || *returned.Lobby.HostPlayerID != fixture.host.PlayerID || !reflect.DeepEqual(returned.StartRoster, started.StartRoster) {
		t.Fatal("started rejoin lost frozen identity", hostAfter)
	}
	outsider := fixture.player(t, "Outsider")
	_, err = fixture.service.Join(context.Background(), outsider, domain.JoinRequest{GameKey: fixture.scope.GameKey, JoinCode: fixture.initial.Lobby.JoinCode, ClientInstanceID: integrationID(t)})
	requireDomainError(t, err, domain.ErrNotFound)
}
func TestLeaveAndJoinCapacityRace(t *testing.T) {
	fixture := newServiceFixture(t, 2)
	departing := fixture.player(t, "Departing")
	fixture.join(t, departing)
	arriving := fixture.player(t, "Arriving")
	client := integrationID(t)
	barrier := make(chan struct{})
	leaveDone := make(chan error, 1)
	joinDone := make(chan error, 1)
	go func() { <-barrier; leaveDone <- fixture.service.Leave(context.Background(), departing, fixture.scope) }()
	go func() {
		<-barrier
		_, err := fixture.service.Join(context.Background(), arriving, domain.JoinRequest{GameKey: fixture.scope.GameKey, JoinCode: fixture.initial.Lobby.JoinCode, ClientInstanceID: client})
		joinDone <- err
	}()
	close(barrier)
	if err := <-leaveDone; err != nil {
		t.Fatal(err)
	}
	joinErr := <-joinDone
	if joinErr != nil && !errors.Is(joinErr, domain.ErrLobbyFull) {
		t.Fatal(joinErr)
	}
	if activeCount(fixture.read(t)) > 2 {
		t.Fatal("capacity overflow")
	}
	if joinErr != nil {
		fixture.join(t, arriving)
	}
	aggregate := fixture.read(t)
	if activeCount(aggregate) != 2 || memberFor(t, aggregate, departing.PlayerID).Active || !memberFor(t, aggregate, arriving.PlayerID).Active {
		t.Fatal("race lost a seat")
	}
}

type tickingIntegrationClock struct {
	mutex sync.Mutex
	now   time.Time
	calls int
}

func (clock *tickingIntegrationClock) Now() time.Time {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()
	value := clock.now
	clock.now = clock.now.Add(time.Second)
	clock.calls++
	return value
}
func TestStartUsesOneLockedTimeCutoff(t *testing.T) {
	fixture := newServiceFixture(t, 3)
	fixture.readyPair(t)
	clock := &tickingIntegrationClock{now: fixture.origin.Add(45*time.Second - time.Microsecond)}
	service, err := application.NewService(fixture.store, clock, domain.CryptoIdentifiers{}, fixture.policy)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Start(context.Background(), fixture.host, fixture.scope)
	if err != nil || result.Lobby.Phase != domain.PhaseStarted || clock.calls != 1 {
		t.Fatal("mutation crossed clock boundaries", clock.calls, err)
	}
}
