package adminmcp_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"islandBattles/lobby/adminmcp"
	"islandBattles/lobby/internal/testutil"
	lobbypostgres "islandBattles/lobby/postgres"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var now = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

type fixedClock struct{}

func (fixedClock) Now() time.Time { return now }

// fakeGames links the waiting lobby to game 7 and counts one running game.
type fakeGames struct{ linkedLobbyID string }

func (games fakeGames) Overview(*gorm.DB, time.Time) (any, error) {
	return map[string]int{"running": 1}, nil
}

func (games fakeGames) GameIDs(_ *gorm.DB, lobbyIDs []string) (map[string]any, error) {
	gameIDs := map[string]any{}
	if slices.Contains(lobbyIDs, games.linkedLobbyID) {
		gameIDs[games.linkedLobbyID] = 7
	}
	return gameIDs, nil
}

// The seeded bearer hash, which no answer may contain.
var bearerHash = bytes.Repeat([]byte{0xAB, 0xCD}, 16)

// seed holds the IDs of the seeded rows.
type seed struct {
	alice, bob, carol, dave, eve string
	waiting, started, closed     string
	blue, red, green             string
}

// seedLobbies creates three lobbies:
//   - waiting (island-battles, ABCD2345): Alice on Blue with a live lease,
//     Bob on Red whose only live lease is of an older generation, Carol in
//     grace after her lease expired, and Eve, who left;
//   - started (quiz-demo): Dave, as "Pal Dave", whose lease was released,
//     on the start roster;
//   - closed (island-battles), with no one.
//
// Alice has a live session and Bob an expired one.
func seedLobbies(t *testing.T, database *gorm.DB) seed {
	t.Helper()
	ids := seed{
		alice: uuid.NewString(), bob: uuid.NewString(), carol: uuid.NewString(), dave: uuid.NewString(), eve: uuid.NewString(),
		waiting: uuid.NewString(), started: uuid.NewString(), closed: uuid.NewString(),
		blue: uuid.NewString(), red: uuid.NewString(), green: uuid.NewString(),
	}
	for index, player := range []struct{ id, name string }{{ids.alice, "Alice"}, {ids.bob, "Bob"}, {ids.carol, "Carol"}, {ids.dave, "Dave"}, {ids.eve, "Eve"}} {
		create(t, database, &lobbypostgres.PlayerRecord{ID: player.id, DisplayName: player.name, CreatedAt: now.Add(-time.Duration(index+1) * time.Hour)})
	}
	create(t, database, &lobbypostgres.SessionRecord{ID: uuid.NewString(), PlayerID: ids.alice, BearerHash: bearerHash, CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(23 * time.Hour)})
	create(t, database, &lobbypostgres.SessionRecord{ID: uuid.NewString(), PlayerID: ids.bob, BearerHash: bytes.Repeat([]byte{1}, 32), CreatedAt: now.Add(-25 * time.Hour), ExpiresAt: now.Add(-time.Hour)})
	alice := ids.alice
	dave := ids.dave
	create(t, database, &lobbypostgres.RoomRecord{
		ID: ids.waiting, GameKey: "island-battles", Name: "Waiting room", JoinCode: "ABCD2345", Capacity: 2, Phase: "waiting", Version: 1,
		HostPlayerID: &alice, CreatedAt: now.Add(-2 * time.Hour), RoundStartedAt: now.Add(-90 * time.Minute), NextJoinSequence: 5, GameSettings: []byte(`{"map_template_id":3}`), LastConnectedAt: now.Add(-time.Minute),
	})
	create(t, database, &lobbypostgres.RoomRecord{
		ID: ids.started, GameKey: "quiz-demo", Name: "Started room", JoinCode: "EFGH2345", Capacity: 2, Phase: "started", Version: 3,
		HostPlayerID: &dave, CreatedAt: now.Add(-time.Hour), RoundStartedAt: now.Add(-time.Hour), NextJoinSequence: 2, GameSettings: []byte(`{}`), LastConnectedAt: now.Add(-10 * time.Minute),
	})
	create(t, database, &lobbypostgres.RoomRecord{
		ID: ids.closed, GameKey: "island-battles", Name: "Closed room", JoinCode: "JKMN2345", Capacity: 2, Phase: "closed", Version: 2,
		CreatedAt: now.Add(-3 * time.Hour), RoundStartedAt: now.Add(-3 * time.Hour), NextJoinSequence: 1, GameSettings: []byte(`{}`), LastConnectedAt: now.Add(-3 * time.Hour),
	})
	create(t, database, &lobbypostgres.TeamRecord{ID: ids.blue, LobbyID: ids.waiting, Name: "Blue", NormalizedName: "blue", TeamOrder: 1})
	create(t, database, &lobbypostgres.TeamRecord{ID: ids.red, LobbyID: ids.waiting, Name: "Red", NormalizedName: "red", TeamOrder: 2})
	create(t, database, &lobbypostgres.TeamRecord{ID: ids.green, LobbyID: ids.started, Name: "Green", NormalizedName: "green", TeamOrder: 1})
	blue, red, green := ids.blue, ids.red, ids.green
	graceEnds := now.Add(30 * time.Second)
	memberships := []lobbypostgres.MembershipRecord{
		{ID: uuid.NewString(), LobbyID: ids.waiting, PlayerID: ids.alice, DisplayName: "Alice", Generation: 1, Active: true, TeamID: &blue, Ready: true, JoinedAt: now.Add(-2 * time.Hour), JoinSequence: 1},
		{ID: uuid.NewString(), LobbyID: ids.waiting, PlayerID: ids.bob, DisplayName: "Bob", Generation: 2, Active: true, TeamID: &red, JoinedAt: now.Add(-90 * time.Minute), JoinSequence: 2},
		{ID: uuid.NewString(), LobbyID: ids.waiting, PlayerID: ids.carol, DisplayName: "Carol", Generation: 1, Active: true, JoinedAt: now.Add(-80 * time.Minute), JoinSequence: 3, DisconnectedUntil: &graceEnds},
		{ID: uuid.NewString(), LobbyID: ids.waiting, PlayerID: ids.eve, DisplayName: "Eve", Generation: 1, JoinedAt: now.Add(-70 * time.Minute), JoinSequence: 4},
		{ID: uuid.NewString(), LobbyID: ids.started, PlayerID: ids.dave, DisplayName: "Pal Dave", Generation: 1, Active: true, TeamID: &green, Ready: true, JoinedAt: now.Add(-time.Hour), JoinSequence: 1},
	}
	for index := range memberships {
		create(t, database, &memberships[index])
	}
	released := now.Add(-5 * time.Minute)
	leases := []lobbypostgres.LeaseRecord{
		{LobbyID: ids.waiting, PlayerID: ids.alice, Generation: 1, ClientInstanceID: uuid.NewString(), ExpiresAt: now.Add(40 * time.Second)},
		{LobbyID: ids.waiting, PlayerID: ids.alice, Generation: 1, ClientInstanceID: uuid.NewString(), ExpiresAt: now.Add(10 * time.Second)},
		{LobbyID: ids.waiting, PlayerID: ids.bob, Generation: 1, ClientInstanceID: uuid.NewString(), ExpiresAt: now.Add(40 * time.Second)},
		{LobbyID: ids.waiting, PlayerID: ids.carol, Generation: 1, ClientInstanceID: uuid.NewString(), ExpiresAt: now.Add(-30 * time.Second)},
		{LobbyID: ids.started, PlayerID: ids.dave, Generation: 1, ClientInstanceID: uuid.NewString(), ExpiresAt: now.Add(40 * time.Second), ReleasedAt: &released},
	}
	for index := range leases {
		create(t, database, &leases[index])
	}
	create(t, database, &lobbypostgres.StartRosterRecord{LobbyID: ids.started, PlayerID: ids.dave, TeamID: ids.green, MemberID: memberships[4].ID, TeamOrder: 1})
	return ids
}

func create(t *testing.T, database *gorm.DB, record any) {
	t.Helper()
	if err := database.Create(record).Error; err != nil {
		t.Fatal(err)
	}
}

// openReadOnly connects to the fixture's database the way the admin server
// does.
func openReadOnly(t *testing.T, owner *gorm.DB) *gorm.DB {
	t.Helper()
	dialector, ok := owner.Dialector.(*gormpostgres.Dialector)
	if !ok {
		t.Fatalf("unexpected dialector %T", owner.Dialector)
	}
	database, err := adminmcp.OpenDatabase(context.Background(), dialector.DSN, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	sqlDatabase, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDatabase.Close() })
	return database
}

// connect serves the lobby tools over an in-memory transport and returns
// the client's session.
func connect(t *testing.T, database *gorm.DB, options adminmcp.Options) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "lobby-admin-test", Version: "test"}, nil)
	adminmcp.AddTools(server, database, options)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "lobby-admin-test-client", Version: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// answers collects every answer's text, to check that none holds a secret.
type answers struct{ texts []string }

// call calls a tool that must succeed and decodes its answer into T.
func call[T any](t *testing.T, session *mcp.ClientSession, collected *answers, name string, arguments map[string]any) T {
	t.Helper()
	text, isError := callText(t, session, name, arguments)
	collected.texts = append(collected.texts, text)
	if isError {
		t.Fatalf("%s(%v) failed: %s", name, arguments, text)
	}
	var answer T
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&answer); err != nil {
		t.Fatalf("decode %s's answer %s: %v", name, text, err)
	}
	return answer
}

// callFailing calls a tool that must fail and returns its error text.
func callFailing(t *testing.T, session *mcp.ClientSession, name string, arguments map[string]any) string {
	t.Helper()
	text, isError := callText(t, session, name, arguments)
	if !isError {
		t.Fatalf("%s(%v) succeeded with %s", name, arguments, text)
	}
	return text
}

func callText(t *testing.T, session *mcp.ClientSession, name string, arguments map[string]any) (string, bool) {
	t.Helper()
	if arguments == nil {
		arguments = map[string]any{}
	}
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	if len(result.Content) != 1 {
		t.Fatalf("%s answered with %d contents", name, len(result.Content))
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("%s answered with %T", name, result.Content[0])
	}
	return text.Text, result.IsError
}

func TestLobbyTools(t *testing.T) {
	owner := testutil.NewPostgresFixture(t)
	ids := seedLobbies(t, owner)
	session := connect(t, openReadOnly(t, owner), adminmcp.Options{
		Clock: fixedClock{}, Games: fakeGames{linkedLobbyID: ids.waiting}, GamesOverview: "running games",
	})
	collected := &answers{}

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s isn't marked read-only", tool.Name)
		}
	}
	slices.Sort(names)
	if want := []string{"find_players", "get_lobby", "list_lobbies", "overview"}; !slices.Equal(names, want) {
		t.Fatalf("tools = %v, want %v", names, want)
	}

	t.Run("overview", func(t *testing.T) {
		overview := call[adminmcp.Overview](t, session, collected, "overview", nil)
		if overview.LobbyCount != 3 || overview.ActiveLobbyCount != 1 || overview.ConnectedMemberCount != 1 || overview.LiveSessionCount != 1 {
			t.Fatalf("overview = %+v, want 3 lobbies, the waiting one active with Alice connected, and Alice's session live", overview)
		}
		want := []adminmcp.LobbyCount{{GameKey: "island-battles", Phase: "closed", Count: 1}, {GameKey: "island-battles", Phase: "waiting", Count: 1}, {GameKey: "quiz-demo", Phase: "started", Count: 1}}
		if !slices.Equal(overview.LobbiesByGameAndPhase, want) {
			t.Errorf("by game and phase = %+v, want %+v", overview.LobbiesByGameAndPhase, want)
		}
		if !overview.Now.Equal(now) || overview.Now.Location() != time.UTC {
			t.Errorf("now = %v", overview.Now)
		}
		if games, ok := overview.Games.(map[string]any); !ok || games["running"] != float64(1) {
			t.Errorf("games = %#v", overview.Games)
		}
	})

	t.Run("list_lobbies", func(t *testing.T) {
		all := call[adminmcp.LobbyList](t, session, collected, "list_lobbies", nil)
		if got := lobbyIDs(all.Lobbies); !slices.Equal(got, []string{ids.started, ids.waiting, ids.closed}) || all.Matching != 3 || all.Truncated {
			t.Fatalf("list = %v (matching %d, truncated %t), want the three lobbies newest first", got, all.Matching, all.Truncated)
		}
		waiting := all.Lobbies[1]
		if waiting.MemberCount != 3 || waiting.ConnectedMemberCount != 1 || waiting.HostDisplayName == nil || *waiting.HostDisplayName != "Alice" ||
			waiting.GameID != float64(7) || waiting.JoinCode != "ABCD2345" || waiting.Capacity != 2 || !waiting.LastConnectedAt.Equal(now.Add(-time.Minute)) {
			t.Errorf("waiting lobby = %+v", waiting)
		}
		if closed := all.Lobbies[2]; closed.HostDisplayName != nil || closed.GameID != nil || closed.MemberCount != 0 {
			t.Errorf("closed lobby = %+v", closed)
		}
		if started := all.Lobbies[0]; started.MemberCount != 1 || started.ConnectedMemberCount != 0 {
			t.Errorf("a released lease still counts as connected: %+v", started)
		}

		active := call[adminmcp.LobbyList](t, session, collected, "list_lobbies", map[string]any{"active_only": true})
		if got := lobbyIDs(active.Lobbies); !slices.Equal(got, []string{ids.waiting}) || active.Matching != 1 {
			t.Errorf("active lobbies = %v, want only the waiting one", got)
		}
		quiz := call[adminmcp.LobbyList](t, session, collected, "list_lobbies", map[string]any{"game_key": "quiz-demo"})
		if got := lobbyIDs(quiz.Lobbies); !slices.Equal(got, []string{ids.started}) {
			t.Errorf("quiz-demo lobbies = %v", got)
		}
		closed := call[adminmcp.LobbyList](t, session, collected, "list_lobbies", map[string]any{"game_key": "island-battles", "phase": "closed"})
		if got := lobbyIDs(closed.Lobbies); !slices.Equal(got, []string{ids.closed}) {
			t.Errorf("closed island-battles lobbies = %v", got)
		}
		limited := call[adminmcp.LobbyList](t, session, collected, "list_lobbies", map[string]any{"limit": 1})
		if len(limited.Lobbies) != 1 || limited.Matching != 3 || !limited.Truncated {
			t.Errorf("limited list = %+v, want one lobby of 3, truncated", limited)
		}
		for _, arguments := range []map[string]any{{"phase": "playing"}, {"limit": 201}, {"limit": -1}} {
			callFailing(t, session, "list_lobbies", arguments)
		}
	})

	t.Run("get_lobby", func(t *testing.T) {
		lobby := call[adminmcp.LobbyDetail](t, session, collected, "get_lobby", map[string]any{"join_code": " abcd2345 "})
		if lobby.ID != ids.waiting || lobby.MemberCount != 3 || lobby.ConnectedMemberCount != 1 || lobby.GameID != float64(7) {
			t.Fatalf("lobby = %+v", lobby.LobbySummary)
		}
		if settings, ok := lobby.GameSettings.(map[string]any); !ok || settings["map_template_id"] != float64(3) {
			t.Errorf("game settings = %#v", lobby.GameSettings)
		}
		wantTeams := []adminmcp.Team{{ID: ids.blue, Name: "Blue", Order: 1, MemberCount: 1}, {ID: ids.red, Name: "Red", Order: 2, MemberCount: 1}}
		if !slices.Equal(lobby.Teams, wantTeams) {
			t.Errorf("teams = %+v, want %+v", lobby.Teams, wantTeams)
		}
		if len(lobby.Members) != 4 {
			t.Fatalf("members = %+v, want 4 in join order", lobby.Members)
		}
		alice, bob, carol, eve := lobby.Members[0], lobby.Members[1], lobby.Members[2], lobby.Members[3]
		if alice.PlayerID != ids.alice || !alice.Connected || !alice.Ready || !alice.Active || alice.TeamName == nil || *alice.TeamName != "Blue" ||
			alice.LeaseExpiresAt == nil || !alice.LeaseExpiresAt.Equal(now.Add(40*time.Second)) || alice.DisconnectedUntil != nil {
			t.Errorf("Alice = %+v, want connected on Blue with her latest lease", alice)
		}
		if bob.Connected || !bob.Active || bob.LeaseExpiresAt != nil {
			t.Errorf("Bob = %+v: a lease of an older generation doesn't connect him", bob)
		}
		if carol.Connected || !carol.Active || carol.DisconnectedUntil == nil || !carol.DisconnectedUntil.Equal(now.Add(30*time.Second)) ||
			carol.LeaseExpiresAt == nil || !carol.LeaseExpiresAt.Equal(now.Add(-30*time.Second)) {
			t.Errorf("Carol = %+v, want disconnected in grace with her expired lease", carol)
		}
		if eve.Active || eve.Connected || eve.TeamID != nil {
			t.Errorf("Eve = %+v, want inactive", eve)
		}
		// Idle for an hour after the last connection comes before a day's age.
		if want := now.Add(-time.Minute + time.Hour); !lobby.RetentionDeletesAt.Equal(want) {
			t.Errorf("retention deletes at %v, want %v", lobby.RetentionDeletesAt, want)
		}
		if len(lobby.StartRoster) != 0 {
			t.Errorf("a waiting lobby has a start roster: %+v", lobby.StartRoster)
		}

		started := call[adminmcp.LobbyDetail](t, session, collected, "get_lobby", map[string]any{"lobby_id": ids.started})
		if len(started.StartRoster) != 1 {
			t.Fatalf("start roster = %+v", started.StartRoster)
		}
		if place := started.StartRoster[0]; place.PlayerID != ids.dave || place.TeamName == nil || *place.TeamName != "Green" ||
			place.DisplayName == nil || *place.DisplayName != "Pal Dave" || place.TeamOrder != 1 {
			t.Errorf("roster place = %+v", place)
		}
		if started.GameID != nil {
			t.Errorf("game ID = %v, want null", started.GameID)
		}

		for _, arguments := range []map[string]any{{}, {"lobby_id": "not-a-uuid"}, {"lobby_id": uuid.NewString()}, {"join_code": "ZZZZ2345"}, {"lobby_id": ids.waiting, "join_code": "ABCD2345"}} {
			callFailing(t, session, "get_lobby", arguments)
		}
	})

	t.Run("find_players", func(t *testing.T) {
		byID := call[adminmcp.PlayerList](t, session, collected, "find_players", map[string]any{"player_id": ids.alice})
		if len(byID.Players) != 1 || byID.Truncated {
			t.Fatalf("players = %+v", byID)
		}
		alice := byID.Players[0]
		if alice.DisplayName != "Alice" || len(alice.LiveSessions) != 1 || !alice.LiveSessions[0].ExpiresAt.Equal(now.Add(23*time.Hour)) ||
			len(alice.Lobbies) != 1 || alice.Lobbies[0].LobbyID != ids.waiting || alice.Lobbies[0].Kind != "human" || !alice.Lobbies[0].Connected || len(alice.RosterPlaces) != 0 {
			t.Errorf("Alice = %+v", alice)
		}
		bob := call[adminmcp.PlayerList](t, session, collected, "find_players", map[string]any{"player_id": ids.bob}).Players[0]
		if len(bob.LiveSessions) != 0 || bob.Lobbies[0].Connected {
			t.Errorf("Bob = %+v, want his expired session left out and him not connected", bob)
		}

		// A name in a lobby counts as well as the player's own.
		pal := call[adminmcp.PlayerList](t, session, collected, "find_players", map[string]any{"display_name": "pAL"})
		if len(pal.Players) != 1 || pal.Players[0].ID != ids.dave {
			t.Fatalf("players named pal = %+v", pal.Players)
		}
		if places := pal.Players[0].RosterPlaces; len(places) != 1 || places[0].LobbyID != ids.started || places[0].TeamName == nil || *places[0].TeamName != "Green" {
			t.Errorf("Dave's roster places = %+v", places)
		}
		named := call[adminmcp.PlayerList](t, session, collected, "find_players", map[string]any{"display_name": "a", "limit": 2})
		if len(named.Players) != 2 || !named.Truncated || named.Players[0].ID != ids.alice || named.Players[1].ID != ids.carol {
			t.Errorf("players named a = %+v, want the newest two of Alice, Carol and Dave", named)
		}
		if wildcard := call[adminmcp.PlayerList](t, session, collected, "find_players", map[string]any{"display_name": "%"}); len(wildcard.Players) != 0 {
			t.Errorf("%% matched %d players as a wildcard", len(wildcard.Players))
		}
		for _, arguments := range []map[string]any{{}, {"player_id": "x"}, {"player_id": ids.alice, "display_name": "Alice"}, {"display_name": "a", "limit": 101}} {
			callFailing(t, session, "find_players", arguments)
		}
	})

	t.Run("no answer holds a bearer hash", func(t *testing.T) {
		for _, text := range collected.texts {
			for _, secret := range []string{"bearer_hash", hex.EncodeToString(bearerHash), base64.StdEncoding.EncodeToString(bearerHash)} {
				if strings.Contains(strings.ToLower(text), strings.ToLower(secret)) {
					t.Errorf("an answer contains %s: %s", secret, text)
				}
			}
		}
	})
}

func lobbyIDs(lobbies []adminmcp.LobbySummary) []string {
	ids := make([]string, len(lobbies))
	for index, lobby := range lobbies {
		ids[index] = lobby.ID
	}
	return ids
}

func TestReadOnlyTransactionsRefuseWrites(t *testing.T) {
	owner := testutil.NewPostgresFixture(t)
	database := openReadOnly(t, owner)
	ctx := context.Background()
	var settings struct {
		TransactionReadOnly string
		StatementTimeout    string
	}
	err := adminmcp.ReadOnly(ctx, database, func(transaction *gorm.DB) error {
		return transaction.Raw("SELECT current_setting('transaction_read_only') AS transaction_read_only, current_setting('statement_timeout') AS statement_timeout").
			Scan(&settings).Error
	})
	if err != nil || settings.TransactionReadOnly != "on" || settings.StatementTimeout != "5s" {
		t.Fatalf("settings = %+v, %v; want a read-only transaction with a 5s statement timeout", settings, err)
	}
	player := lobbypostgres.PlayerRecord{ID: uuid.NewString(), DisplayName: "Mallory", CreatedAt: now}
	err = adminmcp.ReadOnly(ctx, database, func(transaction *gorm.DB) error { return transaction.Create(&player).Error })
	if err == nil || !strings.Contains(err.Error(), "read-only transaction") {
		t.Fatalf("insert in a read-only transaction: %v", err)
	}
	// Outside ReadOnly, the session's own default still refuses writes.
	if err := database.Create(&player).Error; err == nil || !strings.Contains(err.Error(), "read-only transaction") {
		t.Fatalf("insert on a read-only session: %v", err)
	}
	var count int64
	if err := owner.Model(&lobbypostgres.PlayerRecord{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("players = %d, %v; want none written", count, err)
	}
}

// Every tool runs in ReadOnly, so a session-level advisory lock that any of
// them takes ends with the call, although the rollback leaves it.
func TestReadOnlyReleasesAdvisoryLocks(t *testing.T) {
	owner := testutil.NewPostgresFixture(t)
	database := openReadOnly(t, owner)
	err := adminmcp.ReadOnly(context.Background(), database, func(transaction *gorm.DB) error {
		return transaction.Exec("SELECT pg_advisory_lock(7), pg_advisory_lock_shared(8)").Error
	})
	if err != nil {
		t.Fatal(err)
	}
	var locked bool
	if err := owner.Raw("SELECT pg_try_advisory_lock(7) AND pg_try_advisory_lock(8)").Row().Scan(&locked); err != nil || !locked {
		t.Fatalf("another session can't take the locks after the call: %t, %v", locked, err)
	}
	// The connection still serves the next call.
	if err := adminmcp.ReadOnly(context.Background(), database, func(transaction *gorm.DB) error { return transaction.Exec("SELECT 1").Error }); err != nil {
		t.Fatal(err)
	}
}
