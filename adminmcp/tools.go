package adminmcp

import (
	"context"
	"time"

	"islandBattles/lobby/domain"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gorm.io/gorm"
)

// Games adds a game's own data to the lobby tools' answers.
type Games interface {
	// Overview returns the game's counts for the overview tool, which shows
	// them under "games". It reads in the overview's transaction.
	Overview(transaction *gorm.DB, now time.Time) (any, error)
	// GameIDs returns the ID of the game that each of lobbyIDs is linked to,
	// leaving out lobbies without one. The tools show it as game_id.
	GameIDs(transaction *gorm.DB, lobbyIDs []string) (map[string]any, error)
}

// Options configure AddTools. The zero value works.
type Options struct {
	// Clock says when "now" is. Nil uses the system clock.
	Clock domain.Clock
	// LobbyRetention returns the periods after which retention deletes a
	// game's lobbies. Nil uses the lobby's defaults.
	LobbyRetention func(gameKey string) domain.LobbyRetention
	// Games adds a game's data. Nil leaves game counts and game IDs out.
	Games Games
	// GamesOverview describes what Games.Overview counts, for the overview
	// tool's description, such as "Island Battles games running and finished".
	GamesOverview string
}

// The tools' limits.
const (
	DefaultListLimit   = 50
	MaxListLimit       = 200
	DefaultPlayerLimit = 20
	MaxPlayerLimit     = 100
)

// ReadOnlyAnnotations mark a tool as one that changes nothing and reads only
// the database. Every admin tool carries them.
func ReadOnlyAnnotations(title string) *mcp.ToolAnnotations {
	closedWorld := false
	return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld}
}

// AddTools registers overview, list_lobbies, get_lobby and find_players on
// server. Each call reads database in a read-only transaction; see ReadOnly.
func AddTools(server *mcp.Server, database *gorm.DB, options Options) {
	reader := lobbyReader{clock: options.Clock, lobbyRetention: options.LobbyRetention, games: options.Games}
	if reader.clock == nil {
		reader.clock = domain.SystemClock{}
	}
	if reader.lobbyRetention == nil {
		reader.lobbyRetention = domain.DefaultPolicy().LobbyRetention
	}
	overviewDescription := "Answers how many lobbies, players and games there are right now, such as \"How many lobbies are currently active?\". " +
		"Returns lobby counts by game key and phase; active lobbies, which have at least one connected member; members connected now; and live guest sessions. " +
		"A member is connected while one of its leases has not expired or been released."
	if options.GamesOverview != "" {
		overviewDescription += " Under games: " + options.GamesOverview + "."
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "overview",
		Description: overviewDescription,
		Annotations: ReadOnlyAnnotations("Overview"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, Overview, error) {
		var overview Overview
		err := ReadOnly(ctx, database, func(transaction *gorm.DB) error {
			var err error
			overview, err = reader.overview(transaction)
			return err
		})
		return nil, overview, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "list_lobbies",
		Description: "Lists lobbies, newest first, optionally only one game's, one phase's (waiting, started or closed) or only active ones, which have a connected member. " +
			"For each: ID, name, join code, game key, phase, capacity, member count (active members, who hold a seat), connected member count, the host's display name, " +
			"creation and last-connected times, and the linked game's ID. Use get_lobby for one lobby's teams and members.",
		Annotations: ReadOnlyAnnotations("List lobbies"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input ListLobbiesInput) (*mcp.CallToolResult, LobbyList, error) {
		var list LobbyList
		err := ReadOnly(ctx, database, func(transaction *gorm.DB) error {
			var err error
			list, err = reader.listLobbies(transaction, input)
			return err
		})
		return nil, list, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "get_lobby",
		Description: "Describes one lobby, found by lobby_id or join_code, such as \"How many players are in lobby X?\" (member_count, or connected_member_count for those connected now). " +
			"Returns everything list_lobbies does, plus its game settings, its teams with capacity and order, every member with display name, player ID, team, ready, " +
			"active, connected, lease expiry and grace deadline (disconnected_until), the start roster, and when retention deletes the lobby unless a member connects first.",
		Annotations: ReadOnlyAnnotations("Get lobby"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input GetLobbyInput) (*mcp.CallToolResult, LobbyDetail, error) {
		var detail LobbyDetail
		err := ReadOnly(ctx, database, func(transaction *gorm.DB) error {
			var err error
			detail, err = reader.getLobby(transaction, input)
			return err
		})
		return nil, detail, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "find_players",
		Description: "Finds players by player_id, or by display_name as a case-insensitive substring of their own or a lobby display name. " +
			"For each: ID, display name, live guest sessions (creation and expiry only), the lobbies they are or were members of, and their places on lobbies' start rosters.",
		Annotations: ReadOnlyAnnotations("Find players"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input FindPlayersInput) (*mcp.CallToolResult, PlayerList, error) {
		var list PlayerList
		err := ReadOnly(ctx, database, func(transaction *gorm.DB) error {
			var err error
			list, err = reader.findPlayers(transaction, input)
			return err
		})
		return nil, list, err
	})
}
