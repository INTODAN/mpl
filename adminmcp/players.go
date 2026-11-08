package adminmcp

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"islandBattles/lobby/postgres"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// FindPlayersInput names the players to find.
type FindPlayersInput struct {
	PlayerID    string `json:"player_id,omitempty" jsonschema:"The player's ID, a UUID."`
	DisplayName string `json:"display_name,omitempty" jsonschema:"Part of a display name, matched without regard to case, instead of player_id."`
	Limit       int    `json:"limit,omitempty" jsonschema:"The most players to return, 20 by default and at most 100."`
}

// Session is a guest session, without its credential.
type Session struct {
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// PlayerLobby is a player's membership in one lobby.
type PlayerLobby struct {
	LobbyID     string  `json:"lobby_id"`
	LobbyName   string  `json:"lobby_name"`
	GameKey     string  `json:"game_key"`
	Phase       string  `json:"phase"`
	DisplayName string  `json:"display_name" jsonschema:"The player's name in this lobby."`
	Kind        string  `json:"kind" jsonschema:"human for a person, seat for a seat that no person plays, such as a game's bot."`
	TeamID      *string `json:"team_id"`
	Ready       bool    `json:"ready"`
	Active      bool    `json:"active"`
	Connected   bool    `json:"connected"`
}

// Player is one player that find_players found.
type Player struct {
	ID           string        `json:"id"`
	DisplayName  string        `json:"display_name"`
	CreatedAt    time.Time     `json:"created_at"`
	LiveSessions []Session     `json:"live_sessions"`
	Lobbies      []PlayerLobby `json:"lobbies"`
	RosterPlaces []RosterPlace `json:"roster_places" jsonschema:"The player's places on started lobbies' start rosters."`
}

// PlayerList is find_players' answer.
type PlayerList struct {
	Players   []Player `json:"players"`
	Truncated bool     `json:"truncated" jsonschema:"Whether the limit left players out."`
}

// sessionColumns are the only session columns any tool reads. The bearer
// hash is not among them, and the production read-only role may read only
// these.
var sessionColumns = []string{"ID", "PlayerID", "CreatedAt", "ExpiresAt"}

func (reader lobbyReader) findPlayers(transaction *gorm.DB, input FindPlayersInput) (PlayerList, error) {
	limit, err := limitWithin(input.Limit, DefaultPlayerLimit, MaxPlayerLimit)
	if err != nil {
		return PlayerList{}, err
	}
	var matching clause.Expression
	switch {
	case input.PlayerID != "" && input.DisplayName == "":
		if err := uuid.Validate(input.PlayerID); err != nil {
			return PlayerList{}, errors.New("player_id must be a UUID")
		}
		matching = clause.Eq{Column: idColumn, Value: input.PlayerID}
	case input.DisplayName != "" && input.PlayerID == "":
		matching = named(transaction, input.DisplayName)
	default:
		return PlayerList{}, errors.New("pass either player_id or display_name")
	}
	var players []postgres.PlayerRecord
	// One more than the limit says whether the limit left players out.
	if err := transaction.Where(matching).
		Order(clause.OrderByColumn{Column: createdAtColumn, Desc: true}).Order(clause.OrderByColumn{Column: idColumn}).
		Limit(limit + 1).Find(&players).Error; err != nil {
		return PlayerList{}, fmt.Errorf("read players: %w", err)
	}
	list := PlayerList{Players: make([]Player, 0, min(limit, len(players))), Truncated: len(players) > limit}
	players = players[:min(limit, len(players))]
	found := make([]any, len(players))
	for index, player := range players {
		found[index] = player.ID
	}
	inPlayers := clause.IN{Column: playerIDColumn, Values: found}
	now := reader.clock.Now().UTC()
	var sessions []postgres.SessionRecord
	if err := transaction.Select(sessionColumns).Where(inPlayers, clause.Gt{Column: expiresAtColumn, Value: now}).
		Order(clause.OrderByColumn{Column: createdAtColumn}).Find(&sessions).Error; err != nil {
		return PlayerList{}, fmt.Errorf("read sessions: %w", err)
	}
	var memberships []postgres.MembershipRecord
	if err := transaction.Where(inPlayers).Order(clause.OrderByColumn{Column: clause.Column{Name: "joined_at"}, Desc: true}).
		Find(&memberships).Error; err != nil {
		return PlayerList{}, fmt.Errorf("read memberships: %w", err)
	}
	var roster []postgres.StartRosterRecord
	if err := transaction.Where(inPlayers).Find(&roster).Error; err != nil {
		return PlayerList{}, fmt.Errorf("read start rosters: %w", err)
	}
	lobbyIDs := make([]any, 0, len(memberships)+len(roster))
	for _, membership := range memberships {
		lobbyIDs = append(lobbyIDs, membership.LobbyID)
	}
	for _, place := range roster {
		lobbyIDs = append(lobbyIDs, place.LobbyID)
	}
	inLobbies := clause.IN{Column: lobbyIDColumn, Values: lobbyIDs}
	var rooms []postgres.RoomRecord
	if err := transaction.Select("ID", "Name", "GameKey", "Phase").Where(clause.IN{Column: idColumn, Values: lobbyIDs}).
		Find(&rooms).Error; err != nil {
		return PlayerList{}, fmt.Errorf("read lobbies: %w", err)
	}
	var teams []postgres.TeamRecord
	if err := transaction.Select("ID", "Name").Where(inLobbies).Find(&teams).Error; err != nil {
		return PlayerList{}, fmt.Errorf("read teams: %w", err)
	}
	var leases []postgres.LeaseRecord
	if err := transaction.Where(inPlayers, clause.Gt{Column: expiresAtColumn, Value: now}, clause.Eq{Column: releasedAtColumn, Value: nil}).
		Find(&leases).Error; err != nil {
		return PlayerList{}, fmt.Errorf("read leases: %w", err)
	}
	connected := connectedMembers(memberships, leases)
	roomsByID := make(map[string]postgres.RoomRecord, len(rooms))
	for _, room := range rooms {
		roomsByID[room.ID] = room
	}
	teamNames := make(map[string]string, len(teams))
	for _, team := range teams {
		teamNames[team.ID] = team.Name
	}
	memberNames := make(map[string]string, len(memberships))
	for _, membership := range memberships {
		memberNames[membership.ID] = membership.DisplayName
	}
	for _, record := range players {
		player := Player{
			ID: record.ID, DisplayName: record.DisplayName, CreatedAt: record.CreatedAt.UTC(),
			LiveSessions: []Session{}, Lobbies: []PlayerLobby{}, RosterPlaces: []RosterPlace{},
		}
		for _, session := range sessions {
			if session.PlayerID == record.ID {
				player.LiveSessions = append(player.LiveSessions, Session{CreatedAt: session.CreatedAt.UTC(), ExpiresAt: session.ExpiresAt.UTC()})
			}
		}
		for _, membership := range memberships {
			if membership.PlayerID != record.ID {
				continue
			}
			room := roomsByID[membership.LobbyID]
			player.Lobbies = append(player.Lobbies, PlayerLobby{
				LobbyID: membership.LobbyID, LobbyName: room.Name, GameKey: room.GameKey, Phase: room.Phase,
				DisplayName: membership.DisplayName, Kind: membership.Kind, TeamID: membership.TeamID, Ready: membership.Ready, Active: membership.Active,
				Connected: connected[memberKey{membership.LobbyID, membership.PlayerID}],
			})
		}
		for _, place := range roster {
			if place.PlayerID != record.ID {
				continue
			}
			memberID, teamID := place.MemberID, place.TeamID
			player.RosterPlaces = append(player.RosterPlaces, RosterPlace{
				LobbyID: place.LobbyID, PlayerID: place.PlayerID, DisplayName: lookUp(memberNames, &memberID),
				MemberID: place.MemberID, TeamID: place.TeamID, TeamName: lookUp(teamNames, &teamID), TeamOrder: place.TeamOrder,
			})
		}
		list.Players = append(list.Players, player)
	}
	return list, nil
}

// named matches the players whose own display name, or whose name in any
// lobby, contains part, without regard to case.
func named(transaction *gorm.DB, part string) clause.Expression {
	pattern := "%" + likeEscaper.Replace(part) + "%"
	containsPart := clause.Expr{SQL: "? ILIKE ?", Vars: []any{displayNameColumn, pattern}}
	members := transaction.Session(&gorm.Session{NewDB: true}).Model(&postgres.MembershipRecord{}).Select("PlayerID").Where(containsPart)
	return clause.Or(containsPart, clause.Expr{SQL: "? IN (?)", Vars: []any{idColumn, members}})
}

// likeEscaper makes a display name match itself in a LIKE pattern, whose
// default escape character is the backslash.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
