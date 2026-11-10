package application_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"islandBattles/lobby/domain"
)

// roundsGame stands in for a game that plays one game per lobby round. It
// records which rounds have ended, by lobby, and the rounds it was asked
// about. during runs once, inside the next check, to change the lobby while
// the game is asked.
type roundsGame struct {
	mutex   sync.Mutex
	ended   map[string]map[int64]bool
	failing map[string]bool
	asked   map[string][]int64
	during  map[string]func()
}

var replayGame = &roundsGame{ended: map[string]map[int64]bool{}, failing: map[string]bool{}, asked: map[string][]int64{}, during: map[string]func(){}}

func (game *roundsGame) end(lobbyID string, round int64) {
	game.mutex.Lock()
	defer game.mutex.Unlock()
	if game.ended[lobbyID] == nil {
		game.ended[lobbyID] = map[int64]bool{}
	}
	game.ended[lobbyID][round] = true
}

func (game *roundsGame) askedRounds(lobbyID string) []int64 {
	game.mutex.Lock()
	defer game.mutex.Unlock()
	return append([]int64(nil), game.asked[lobbyID]...)
}

func (game *roundsGame) gameOver(_ context.Context, lobbyID string, round int64) (bool, error) {
	game.mutex.Lock()
	game.asked[lobbyID] = append(game.asked[lobbyID], round)
	during := game.during[lobbyID]
	delete(game.during, lobbyID)
	failing := game.failing[lobbyID]
	game.mutex.Unlock()
	if during != nil {
		during()
	}
	if failing {
		return false, errors.New("the game's state is unreadable")
	}
	game.mutex.Lock()
	defer game.mutex.Unlock()
	return game.ended[lobbyID][round], nil
}

func replayLobby(request *domain.CreateRequest) { request.GameKey = "replay-game" }

// startRound gets the host and guest ready on teams 0 and 1 and starts.
func (fixture *serviceFixture) startRound(t *testing.T, guest domain.Principal) domain.Snapshot {
	t.Helper()
	fixture.assign(t, fixture.host, 0, true)
	fixture.assign(t, guest, 1, true)
	snapshot, err := fixture.service.Start(context.Background(), fixture.host, fixture.scope)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestReopenAfterTheGameEndsPlaysAnotherRound(t *testing.T) {
	fixture := newServiceFixture(t, 4, replayLobby)
	guest := fixture.player(t, "Guest")
	fixture.join(t, guest)
	seat := fixture.addSeat(t, 2, `{"level":2}`)
	started := fixture.startRound(t, guest)
	if started.Lobby.Round != 1 || len(started.StartRoster) != 3 {
		t.Fatalf("first start = round %d, roster %v", started.Lobby.Round, started.StartRoster)
	}

	before := fixture.read(t)
	pushes := fixture.pushed(invalidationPushes)
	_, err := fixture.service.Reopen(context.Background(), guest, fixture.scope)
	requireDomainError(t, err, domain.ErrGameRunning)
	if !reflect.DeepEqual(before, fixture.read(t)) || fixture.pushed(invalidationPushes) != pushes {
		t.Fatal("reopening a running game changed the lobby")
	}

	replayGame.end(fixture.scope.LobbyID, 1)
	reopened, err := fixture.service.Reopen(context.Background(), guest, fixture.scope)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Lobby.Phase != domain.PhaseWaiting || reopened.Lobby.Round != 2 || len(reopened.StartRoster) != 0 || reopened.Lobby.Version != before.Lobby.Version+1 {
		t.Fatalf("reopened = phase %s, round %d, roster %v, version %d", reopened.Lobby.Phase, reopened.Lobby.Round, reopened.StartRoster, reopened.Lobby.Version)
	}
	if fixture.pushed(invalidationPushes) != pushes+1 {
		t.Fatal("reopening published no lobby.changed")
	}
	after := fixture.read(t)
	if !reflect.DeepEqual(after.Teams, before.Teams) || string(after.Lobby.GameSettings) != string(before.Lobby.GameSettings) || *after.Lobby.HostPlayerID != fixture.host.PlayerID {
		t.Fatal("reopening changed the teams, the settings or the host")
	}
	for index, member := range after.Members {
		previous := before.Members[index]
		if member.ID != previous.ID || member.Active != previous.Active || !reflect.DeepEqual(member.TeamID, previous.TeamID) {
			t.Fatalf("member %s changed: %#v, was %#v", member.DisplayName, member, previous)
		}
		if member.Ready != member.IsSeat() {
			t.Fatalf("member %s ready = %v after reopening; people clear it, seats keep it", member.DisplayName, member.Ready)
		}
	}
	if seatMember := memberFor(t, after, seat.Members[len(seat.Members)-1].PlayerID); !seatMember.IsSeat() || !seatMember.Active {
		t.Fatal("the seat left with the reopen")
	}

	// A second call finds the lobby waiting and returns it unchanged.
	again, err := fixture.service.Reopen(context.Background(), fixture.host, fixture.scope)
	if err != nil || again.Lobby.Version != reopened.Lobby.Version || again.Lobby.Round != 2 || fixture.pushed(invalidationPushes) != pushes+1 {
		t.Fatalf("second reopen = version %d round %d, %v", again.Lobby.Version, again.Lobby.Round, err)
	}

	// The lobby waits as before a first game: a new player joins on the free
	// team, everyone gets ready and the host starts the second round.
	newcomer := fixture.player(t, "Newcomer")
	fixture.join(t, newcomer)
	fixture.assign(t, newcomer, 3, true)
	second := fixture.startRound(t, guest)
	if second.Lobby.Phase != domain.PhaseStarted || second.Lobby.Round != 2 || len(second.StartRoster) != 4 {
		t.Fatalf("second start = phase %s, round %d, roster %v", second.Lobby.Phase, second.Lobby.Round, second.StartRoster)
	}
	if !slicesContain(second.StartRoster, newcomer.PlayerID) {
		t.Fatal("the newcomer isn't in the second roster")
	}
	_, err = fixture.service.Reopen(context.Background(), newcomer, fixture.scope)
	requireDomainError(t, err, domain.ErrGameRunning)
	if asked := replayGame.askedRounds(fixture.scope.LobbyID); !reflect.DeepEqual(asked, []int64{1, 1, 2}) {
		t.Fatalf("the game was asked about rounds %v, want [1 1 2]", asked)
	}
}

func slicesContain(roster domain.StartRoster, playerID string) bool {
	for _, entry := range roster {
		if entry.PlayerID == playerID {
			return true
		}
	}
	return false
}

func TestReopenRefusals(t *testing.T) {
	t.Run("a person outside the lobby", func(t *testing.T) {
		fixture := newServiceFixture(t, 2, replayLobby)
		guest := fixture.player(t, "Guest")
		fixture.join(t, guest)
		fixture.startRound(t, guest)
		replayGame.end(fixture.scope.LobbyID, 1)
		_, err := fixture.service.Reopen(context.Background(), fixture.player(t, "Outsider"), fixture.scope)
		requireDomainError(t, err, domain.ErrNotFound)
		if fixture.read(t).Lobby.Phase != domain.PhaseStarted {
			t.Fatal("an outsider reopened the lobby")
		}
		if asked := replayGame.askedRounds(fixture.scope.LobbyID); len(asked) != 0 {
			t.Fatalf("the game was asked for an outsider: %v", asked)
		}
	})
	t.Run("a member who left during the game", func(t *testing.T) {
		fixture := newServiceFixture(t, 2, replayLobby)
		guest := fixture.player(t, "Guest")
		fixture.join(t, guest)
		fixture.startRound(t, guest)
		replayGame.end(fixture.scope.LobbyID, 1)
		if err := fixture.service.Leave(context.Background(), guest, fixture.scope); err != nil {
			t.Fatal(err)
		}
		_, err := fixture.service.Reopen(context.Background(), guest, fixture.scope)
		requireDomainError(t, err, domain.ErrNotFound)
		reopened, err := fixture.service.Reopen(context.Background(), fixture.host, fixture.scope)
		if err != nil || reopened.Lobby.Phase != domain.PhaseWaiting {
			t.Fatalf("host's reopen = %v, %v", reopened.Lobby.Phase, err)
		}
		if member := memberFor(t, fixture.read(t), guest.PlayerID); member.Active {
			t.Fatal("the member who left is back")
		}
	})
	t.Run("a waiting lobby", func(t *testing.T) {
		fixture := newServiceFixture(t, 2, replayLobby)
		before := fixture.read(t)
		snapshot, err := fixture.service.Reopen(context.Background(), fixture.host, fixture.scope)
		if err != nil || snapshot.Lobby.Round != 1 || snapshot.Lobby.Version != before.Lobby.Version {
			t.Fatalf("reopen of a waiting lobby = round %d version %d, %v", snapshot.Lobby.Round, snapshot.Lobby.Version, err)
		}
	})
	t.Run("a game that declares no game-over check", func(t *testing.T) {
		fixture := newServiceFixture(t, 2)
		guest := fixture.player(t, "Guest")
		fixture.join(t, guest)
		fixture.startRound(t, guest)
		_, err := fixture.service.Reopen(context.Background(), guest, fixture.scope)
		requireDomainError(t, err, domain.ErrPhaseConflict)
	})
	t.Run("a closed lobby", func(t *testing.T) {
		fixture := newServiceFixture(t, 2, replayLobby)
		if err := fixture.service.Leave(context.Background(), fixture.host, fixture.scope); err != nil {
			t.Fatal(err)
		}
		if phase := fixture.read(t).Lobby.Phase; phase != domain.PhaseClosed {
			t.Fatalf("phase = %s, want closed", phase)
		}
		// A closed lobby has no active member left to reopen it.
		_, err := fixture.service.Reopen(context.Background(), fixture.host, fixture.scope)
		requireDomainError(t, err, domain.ErrNotFound)
	})
	t.Run("a check that can't read the game", func(t *testing.T) {
		fixture := newServiceFixture(t, 2, replayLobby)
		guest := fixture.player(t, "Guest")
		fixture.join(t, guest)
		fixture.startRound(t, guest)
		replayGame.mutex.Lock()
		replayGame.failing[fixture.scope.LobbyID] = true
		replayGame.mutex.Unlock()
		_, err := fixture.service.Reopen(context.Background(), guest, fixture.scope)
		requireDomainError(t, err, domain.ErrUnavailable)
	})
}

func TestTwoMembersReopenAtOnce(t *testing.T) {
	fixture := newServiceFixture(t, 2, replayLobby)
	guest := fixture.player(t, "Guest")
	fixture.join(t, guest)
	started := fixture.startRound(t, guest)
	replayGame.end(fixture.scope.LobbyID, 1)
	barrier := make(chan struct{})
	results := make(chan error, 2)
	for _, actor := range []domain.Principal{fixture.host, guest} {
		go func() {
			<-barrier
			snapshot, err := fixture.service.Reopen(context.Background(), actor, fixture.scope)
			if err == nil && (snapshot.Lobby.Phase != domain.PhaseWaiting || snapshot.Lobby.Round != 2) {
				err = errors.New("reopen returned another lobby state")
			}
			results <- err
		}()
	}
	close(barrier)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	aggregate := fixture.read(t)
	if aggregate.Lobby.Round != 2 || aggregate.Lobby.Version != started.Lobby.Version+1 {
		t.Fatalf("after two reopens: round %d, version %d (was %d)", aggregate.Lobby.Round, aggregate.Lobby.Version, started.Lobby.Version)
	}
}

// When the lobby moves to another round while the game is asked, the answer
// is about a round that is over, so Reopen asks again about the current one.
func TestReopenAsksAgainWhenTheRoundMoves(t *testing.T) {
	fixture := newServiceFixture(t, 2, replayLobby)
	guest := fixture.player(t, "Guest")
	fixture.join(t, guest)
	fixture.startRound(t, guest)
	replayGame.end(fixture.scope.LobbyID, 1)
	replayGame.mutex.Lock()
	replayGame.during[fixture.scope.LobbyID] = func() {
		// The host reopens and starts round 2 while the guest's request
		// asks the game about round 1.
		if _, err := fixture.service.Reopen(context.Background(), fixture.host, fixture.scope); err != nil {
			t.Error(err)
			return
		}
		fixture.startRound(t, guest)
	}
	replayGame.mutex.Unlock()
	_, err := fixture.service.Reopen(context.Background(), guest, fixture.scope)
	requireDomainError(t, err, domain.ErrGameRunning)
	aggregate := fixture.read(t)
	if aggregate.Lobby.Phase != domain.PhaseStarted || aggregate.Lobby.Round != 2 {
		t.Fatalf("lobby = phase %s, round %d; want round 2 started", aggregate.Lobby.Phase, aggregate.Lobby.Round)
	}
	if asked := replayGame.askedRounds(fixture.scope.LobbyID); !reflect.DeepEqual(asked, []int64{1, 1, 2}) {
		t.Fatalf("the game was asked about rounds %v, want [1 1 2]", asked)
	}
}
