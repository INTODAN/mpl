package adminmcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"islandBattles/lobby/domain"
	"islandBattles/lobby/postgres"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// LobbyCount counts one game's lobbies in one phase.
type LobbyCount struct {
	GameKey string `json:"game_key"`
	Phase   string `json:"phase"`
	Count   int    `json:"lobby_count"`
}

// Overview is the overview tool's answer.
type Overview struct {
	Now                   time.Time    `json:"now"`
	LobbyCount            int          `json:"lobby_count"`
	LobbiesByGameAndPhase []LobbyCount `json:"lobbies_by_game_and_phase"`
	ActiveLobbyCount      int          `json:"active_lobby_count" jsonschema:"Lobbies with at least one connected member."`
	ConnectedMemberCount  int          `json:"connected_member_count" jsonschema:"Members connected now, across all lobbies."`
	LiveSessionCount      int          `json:"live_session_count" jsonschema:"Guest sessions that have not expired."`
	Games                 any          `json:"games,omitempty" jsonschema:"The game's own counts."`
}

// ListLobbiesInput filters list_lobbies.
type ListLobbiesInput struct {
	GameKey    string `json:"game_key,omitempty" jsonschema:"Only this game's lobbies, such as island-battles."`
	Phase      string `json:"phase,omitempty" jsonschema:"Only lobbies in this phase: waiting, started or closed."`
	ActiveOnly bool   `json:"active_only,omitempty" jsonschema:"Only lobbies with at least one connected member."`
	Limit      int    `json:"limit,omitempty" jsonschema:"The most lobbies to return, 50 by default and at most 200."`
}

// LobbySummary is one lobby in list_lobbies, and the head of get_lobby.
type LobbySummary struct {
	ID                   string    `json:"id"`
	Name                 string    `json:"name"`
	JoinCode             string    `json:"join_code"`
	GameKey              string    `json:"game_key"`
	Phase                string    `json:"phase"`
	Capacity             int       `json:"capacity" jsonschema:"The members the lobby holds: its number of teams, since a team holds one member."`
	MemberCount          int       `json:"member_count" jsonschema:"Active members, who hold a seat, connected or not."`
	ConnectedMemberCount int       `json:"connected_member_count" jsonschema:"Active members with a lease that has not expired or been released."`
	HostDisplayName      *string   `json:"host_display_name"`
	CreatedAt            time.Time `json:"created_at"`
	LastConnectedAt      time.Time `json:"last_connected_at" jsonschema:"The last time a member created, joined, heartbeated, released a lease or left."`
	GameID               any       `json:"game_id" jsonschema:"The game started from the lobby, or null."`
}

// LobbyList is list_lobbies' answer.
type LobbyList struct {
	Lobbies []LobbySummary `json:"lobbies"`
	// Matching counts every lobby that matched, before the limit.
	Matching  int  `json:"matching" jsonschema:"Lobbies that matched, before the limit."`
	Truncated bool `json:"truncated" jsonschema:"Whether the limit left lobbies out."`
}

// GetLobbyInput names one lobby.
type GetLobbyInput struct {
	LobbyID  string `json:"lobby_id,omitempty" jsonschema:"The lobby's ID, a UUID."`
	JoinCode string `json:"join_code,omitempty" jsonschema:"The lobby's join code, instead of lobby_id."`
	GameKey  string `json:"game_key,omitempty" jsonschema:"The lobby's game, when a join code is used by several games."`
}

// Team is one of a lobby's teams. It holds at most one member.
type Team struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Order       int    `json:"order" jsonschema:"The team's position, from 1."`
	MemberCount int    `json:"member_count" jsonschema:"Active members on the team."`
}

// Member is one of a lobby's memberships, active or not.
type Member struct {
	ID                string     `json:"id"`
	PlayerID          string     `json:"player_id"`
	DisplayName       string     `json:"display_name"`
	Kind              string     `json:"kind" jsonschema:"human for a person who joined, seat for a seat that no person plays, such as a game's bot, which the host added; a seat holds no lease, so it never shows as connected."`
	TeamID            *string    `json:"team_id"`
	TeamName          *string    `json:"team_name"`
	Ready             bool       `json:"ready"`
	Active            bool       `json:"active" jsonschema:"Whether the member holds a seat. A member who left or whose grace ran out is inactive."`
	Connected         bool       `json:"connected"`
	LeaseExpiresAt    *time.Time `json:"lease_expires_at" jsonschema:"When the member's latest unreleased lease expires, or null."`
	DisconnectedUntil *time.Time `json:"disconnected_until" jsonschema:"The end of the grace period of a member whose leases ended, or null."`
	JoinedAt          time.Time  `json:"joined_at"`
}

// RosterPlace is one place on a started lobby's start roster.
type RosterPlace struct {
	LobbyID     string  `json:"lobby_id"`
	PlayerID    string  `json:"player_id"`
	DisplayName *string `json:"display_name"`
	MemberID    string  `json:"member_id"`
	TeamID      string  `json:"team_id"`
	TeamName    *string `json:"team_name"`
	TeamOrder   int     `json:"team_order"`
}

// LobbyDetail is get_lobby's answer.
type LobbyDetail struct {
	LobbySummary
	GameSettings any           `json:"game_settings" jsonschema:"The settings the lobby was created with."`
	Teams        []Team        `json:"teams"`
	Members      []Member      `json:"members"`
	StartRoster  []RosterPlace `json:"start_roster" jsonschema:"Who started the game, on which team. Empty until the lobby starts."`
	// RetentionDeletesAt is the earlier of the idle and the age deadline.
	RetentionDeletesAt time.Time `json:"retention_deletes_at" jsonschema:"When retention deletes the lobby unless a member connects first, which moves the idle deadline later. The age deadline never moves."`
}

// lobbyReader answers the lobby tools from the lobby tables.
type lobbyReader struct {
	clock          domain.Clock
	lobbyRetention func(gameKey string) domain.LobbyRetention
	games          Games
}

// The lobby tables' columns that the tools filter and order by.
var (
	idColumn           = clause.Column{Name: "id"}
	lobbyIDColumn      = clause.Column{Name: "lobby_id"}
	playerIDColumn     = clause.Column{Name: "player_id"}
	createdAtColumn    = clause.Column{Name: "created_at"}
	expiresAtColumn    = clause.Column{Name: "expires_at"}
	releasedAtColumn   = clause.Column{Name: "released_at"}
	joinSequenceColumn = clause.Column{Name: "join_sequence"}
	teamOrderColumn    = clause.Column{Name: "team_order"}
	displayNameColumn  = clause.Column{Name: "display_name"}
)

// memberKey identifies a membership within a lobby.
type memberKey struct {
	lobbyID  string
	playerID string
}

// liveLeases returns the lobbies' leases that have neither expired nor been
// released by now, for every lobby when lobbyIDs is nil.
func liveLeases(transaction *gorm.DB, lobbyIDs []string, now time.Time) ([]postgres.LeaseRecord, error) {
	query := transaction.Model(&postgres.LeaseRecord{}).Select("LobbyID", "PlayerID", "Generation", "ExpiresAt").
		Where(clause.Gt{Column: expiresAtColumn, Value: now}, clause.Eq{Column: releasedAtColumn, Value: nil})
	if lobbyIDs != nil {
		query = query.Where(clause.IN{Column: lobbyIDColumn, Values: anySlice(lobbyIDs)})
	}
	var leases []postgres.LeaseRecord
	err := query.Find(&leases).Error
	return leases, err
}

// connectedMembers returns the active members that hold a live lease of
// their current generation, as the lobby itself decides who is connected.
// A lease of an older generation belongs to a membership that has since been
// renewed, so it doesn't count.
func connectedMembers(members []postgres.MembershipRecord, leases []postgres.LeaseRecord) map[memberKey]bool {
	generations := make(map[memberKey]int64, len(members))
	for _, member := range members {
		if member.Active {
			generations[memberKey{member.LobbyID, member.PlayerID}] = member.Generation
		}
	}
	connected := map[memberKey]bool{}
	for _, lease := range leases {
		key := memberKey{lease.LobbyID, lease.PlayerID}
		if generation, active := generations[key]; active && generation == lease.Generation {
			connected[key] = true
		}
	}
	return connected
}

func (reader lobbyReader) overview(transaction *gorm.DB) (Overview, error) {
	now := reader.clock.Now().UTC()
	var rooms []postgres.RoomRecord
	if err := transaction.Select("ID", "GameKey", "Phase").Find(&rooms).Error; err != nil {
		return Overview{}, fmt.Errorf("read lobbies: %w", err)
	}
	var members []postgres.MembershipRecord
	if err := transaction.Select("LobbyID", "PlayerID", "Generation", "Active").
		Where(&postgres.MembershipRecord{Active: true}).Find(&members).Error; err != nil {
		return Overview{}, fmt.Errorf("read members: %w", err)
	}
	leases, err := liveLeases(transaction, nil, now)
	if err != nil {
		return Overview{}, fmt.Errorf("read leases: %w", err)
	}
	var liveSessions int64
	if err := transaction.Model(&postgres.SessionRecord{}).Where(clause.Gt{Column: expiresAtColumn, Value: now}).
		Count(&liveSessions).Error; err != nil {
		return Overview{}, fmt.Errorf("count sessions: %w", err)
	}
	connected := connectedMembers(members, leases)
	activeLobbies := map[string]bool{}
	for key := range connected {
		activeLobbies[key.lobbyID] = true
	}
	counts := map[LobbyCount]int{}
	for _, room := range rooms {
		counts[LobbyCount{GameKey: room.GameKey, Phase: room.Phase}]++
	}
	overview := Overview{
		Now: now, LobbyCount: len(rooms), LobbiesByGameAndPhase: make([]LobbyCount, 0, len(counts)),
		ActiveLobbyCount: len(activeLobbies), ConnectedMemberCount: len(connected), LiveSessionCount: int(liveSessions),
	}
	for count, lobbies := range counts {
		count.Count = lobbies
		overview.LobbiesByGameAndPhase = append(overview.LobbiesByGameAndPhase, count)
	}
	slices.SortFunc(overview.LobbiesByGameAndPhase, func(left, right LobbyCount) int {
		return strings.Compare(left.GameKey+"\x00"+left.Phase, right.GameKey+"\x00"+right.Phase)
	})
	if reader.games != nil {
		overview.Games, err = reader.games.Overview(transaction, now)
		if err != nil {
			return Overview{}, fmt.Errorf("read the game's counts: %w", err)
		}
	}
	return overview, nil
}

// limitWithin applies a tool's default and maximum to a requested limit.
func limitWithin(requested, defaultLimit, maxLimit int) (int, error) {
	switch {
	case requested == 0:
		return defaultLimit, nil
	case requested < 0 || requested > maxLimit:
		return 0, fmt.Errorf("limit must be from 1 to %d", maxLimit)
	}
	return requested, nil
}

// summaryColumns are the room columns a summary shows. They leave out the
// game settings, which may be large.
var summaryColumns = []string{"ID", "GameKey", "Name", "JoinCode", "Capacity", "Phase", "HostPlayerID", "CreatedAt", "LastConnectedAt"}

func (reader lobbyReader) listLobbies(transaction *gorm.DB, input ListLobbiesInput) (LobbyList, error) {
	limit, err := limitWithin(input.Limit, DefaultListLimit, MaxListLimit)
	if err != nil {
		return LobbyList{}, err
	}
	if input.Phase != "" && !slices.Contains([]string{string(domain.PhaseWaiting), string(domain.PhaseStarted), string(domain.PhaseClosed)}, input.Phase) {
		return LobbyList{}, errors.New("phase must be waiting, started or closed")
	}
	var rooms []postgres.RoomRecord
	// Zero fields of the filter are left out of the condition.
	if err := transaction.Select(summaryColumns).Where(&postgres.RoomRecord{GameKey: input.GameKey, Phase: input.Phase}).
		Order(clause.OrderByColumn{Column: createdAtColumn, Desc: true}).Order(clause.OrderByColumn{Column: idColumn}).
		Find(&rooms).Error; err != nil {
		return LobbyList{}, fmt.Errorf("read lobbies: %w", err)
	}
	summaries, err := reader.summaries(transaction, rooms)
	if err != nil {
		return LobbyList{}, err
	}
	list := LobbyList{Lobbies: make([]LobbySummary, 0, min(limit, len(summaries)))}
	for _, summary := range summaries {
		if input.ActiveOnly && summary.ConnectedMemberCount == 0 {
			continue
		}
		list.Matching++
		if len(list.Lobbies) < limit {
			list.Lobbies = append(list.Lobbies, summary)
		}
	}
	list.Truncated = list.Matching > len(list.Lobbies)
	return list, nil
}

// summaries summarizes rooms, in their order, with a fixed number of queries.
func (reader lobbyReader) summaries(transaction *gorm.DB, rooms []postgres.RoomRecord) ([]LobbySummary, error) {
	now := reader.clock.Now().UTC()
	lobbyIDs := make([]string, len(rooms))
	for index, room := range rooms {
		lobbyIDs[index] = room.ID
	}
	var members []postgres.MembershipRecord
	if err := transaction.Select("LobbyID", "PlayerID", "DisplayName", "Generation", "Active").
		Where(&postgres.MembershipRecord{Active: true}).Where(clause.IN{Column: lobbyIDColumn, Values: anySlice(lobbyIDs)}).
		Find(&members).Error; err != nil {
		return nil, fmt.Errorf("read members: %w", err)
	}
	leases, err := liveLeases(transaction, lobbyIDs, now)
	if err != nil {
		return nil, fmt.Errorf("read leases: %w", err)
	}
	gameIDs := map[string]any{}
	if reader.games != nil && len(lobbyIDs) > 0 {
		if gameIDs, err = reader.games.GameIDs(transaction, lobbyIDs); err != nil {
			return nil, fmt.Errorf("read the lobbies' games: %w", err)
		}
	}
	connected := connectedMembers(members, leases)
	summaries := make([]LobbySummary, len(rooms))
	for index, room := range rooms {
		summaries[index] = summary(room, gameIDs[room.ID])
	}
	byLobby := make(map[string]*LobbySummary, len(rooms))
	for index := range summaries {
		byLobby[summaries[index].ID] = &summaries[index]
	}
	for _, member := range members {
		lobby := byLobby[member.LobbyID]
		lobby.MemberCount++
		if connected[memberKey{member.LobbyID, member.PlayerID}] {
			lobby.ConnectedMemberCount++
		}
	}
	for index, room := range rooms {
		summaries[index].HostDisplayName = hostDisplayName(room, members)
	}
	return summaries, nil
}

// summary is the part of a lobby's summary that its room row holds.
func summary(room postgres.RoomRecord, gameID any) LobbySummary {
	return LobbySummary{
		ID: room.ID, Name: room.Name, JoinCode: room.JoinCode, GameKey: room.GameKey, Phase: room.Phase, Capacity: room.Capacity,
		CreatedAt: room.CreatedAt.UTC(), LastConnectedAt: room.LastConnectedAt.UTC(), GameID: gameID,
	}
}

// hostDisplayName is the name the host has in the lobby.
func hostDisplayName(room postgres.RoomRecord, members []postgres.MembershipRecord) *string {
	if room.HostPlayerID == nil {
		return nil
	}
	for _, member := range members {
		if member.LobbyID == room.ID && member.PlayerID == *room.HostPlayerID {
			name := member.DisplayName
			return &name
		}
	}
	return nil
}

func (reader lobbyReader) getLobby(transaction *gorm.DB, input GetLobbyInput) (LobbyDetail, error) {
	room, err := findRoom(transaction, input)
	if err != nil {
		return LobbyDetail{}, err
	}
	now := reader.clock.Now().UTC()
	var teams []postgres.TeamRecord
	if err := transaction.Where(&postgres.TeamRecord{LobbyID: room.ID}).
		Order(clause.OrderByColumn{Column: teamOrderColumn}).Find(&teams).Error; err != nil {
		return LobbyDetail{}, fmt.Errorf("read teams: %w", err)
	}
	var members []postgres.MembershipRecord
	if err := transaction.Where(&postgres.MembershipRecord{LobbyID: room.ID}).
		Order(clause.OrderByColumn{Column: joinSequenceColumn}).Find(&members).Error; err != nil {
		return LobbyDetail{}, fmt.Errorf("read members: %w", err)
	}
	var leases []postgres.LeaseRecord
	if err := transaction.Where(&postgres.LeaseRecord{LobbyID: room.ID}).Find(&leases).Error; err != nil {
		return LobbyDetail{}, fmt.Errorf("read leases: %w", err)
	}
	var roster []postgres.StartRosterRecord
	if err := transaction.Where(&postgres.StartRosterRecord{LobbyID: room.ID}).
		Order(clause.OrderByColumn{Column: teamOrderColumn}).Order(clause.OrderByColumn{Column: playerIDColumn}).
		Find(&roster).Error; err != nil {
		return LobbyDetail{}, fmt.Errorf("read the start roster: %w", err)
	}
	gameIDs := map[string]any{}
	if reader.games != nil {
		if gameIDs, err = reader.games.GameIDs(transaction, []string{room.ID}); err != nil {
			return LobbyDetail{}, fmt.Errorf("read the lobby's game: %w", err)
		}
	}
	var settings any
	if len(room.GameSettings) > 0 {
		if err := json.Unmarshal(room.GameSettings, &settings); err != nil {
			return LobbyDetail{}, fmt.Errorf("read the game settings: %w", err)
		}
	}
	live := make([]postgres.LeaseRecord, 0, len(leases))
	for _, lease := range leases {
		if lease.ReleasedAt == nil && lease.ExpiresAt.After(now) {
			live = append(live, lease)
		}
	}
	connected := connectedMembers(members, live)
	teamNames := make(map[string]string, len(teams))
	detail := LobbyDetail{
		LobbySummary: summary(room, gameIDs[room.ID]), GameSettings: settings,
		Teams: make([]Team, 0, len(teams)), Members: make([]Member, 0, len(members)), StartRoster: make([]RosterPlace, 0, len(roster)),
		RetentionDeletesAt: reader.retentionDeadline(room),
	}
	detail.HostDisplayName = hostDisplayName(room, members)
	teamMembers := map[string]int{}
	memberNames := make(map[string]string, len(members))
	for _, member := range members {
		memberNames[member.ID] = member.DisplayName
		if !member.Active {
			continue
		}
		detail.MemberCount++
		if connected[memberKey{member.LobbyID, member.PlayerID}] {
			detail.ConnectedMemberCount++
		}
		if member.TeamID != nil {
			teamMembers[*member.TeamID]++
		}
	}
	for _, team := range teams {
		teamNames[team.ID] = team.Name
		detail.Teams = append(detail.Teams, Team{ID: team.ID, Name: team.Name, Order: team.TeamOrder, MemberCount: teamMembers[team.ID]})
	}
	for _, member := range members {
		detail.Members = append(detail.Members, Member{
			ID: member.ID, PlayerID: member.PlayerID, DisplayName: member.DisplayName, Kind: member.Kind,
			TeamID: member.TeamID, TeamName: lookUp(teamNames, member.TeamID), Ready: member.Ready, Active: member.Active,
			Connected:         connected[memberKey{member.LobbyID, member.PlayerID}],
			LeaseExpiresAt:    latestLeaseExpiry(member, leases),
			DisconnectedUntil: utcPointer(member.DisconnectedUntil), JoinedAt: member.JoinedAt.UTC(),
		})
	}
	for _, place := range roster {
		memberID, teamID := place.MemberID, place.TeamID
		detail.StartRoster = append(detail.StartRoster, RosterPlace{
			LobbyID: place.LobbyID, PlayerID: place.PlayerID, DisplayName: lookUp(memberNames, &memberID),
			MemberID: place.MemberID, TeamID: place.TeamID, TeamName: lookUp(teamNames, &teamID), TeamOrder: place.TeamOrder,
		})
	}
	return detail, nil
}

// findRoom finds the lobby that input names, with all its columns.
func findRoom(transaction *gorm.DB, input GetLobbyInput) (postgres.RoomRecord, error) {
	var rooms []postgres.RoomRecord
	switch {
	case input.LobbyID != "" && input.JoinCode == "":
		if err := uuid.Validate(input.LobbyID); err != nil {
			return postgres.RoomRecord{}, errors.New("lobby_id must be a UUID")
		}
		if err := transaction.Where(&postgres.RoomRecord{ID: input.LobbyID}).Limit(1).Find(&rooms).Error; err != nil {
			return postgres.RoomRecord{}, fmt.Errorf("read the lobby: %w", err)
		}
	case input.JoinCode != "" && input.LobbyID == "":
		joinCode, err := domain.NormalizeJoinCode(input.JoinCode)
		if err != nil {
			return postgres.RoomRecord{}, errors.New("join_code is not a valid join code")
		}
		// Join codes are unique within a game, so two games may share one.
		if err := transaction.Where(&postgres.RoomRecord{JoinCode: joinCode, GameKey: input.GameKey}).
			Limit(2).Find(&rooms).Error; err != nil {
			return postgres.RoomRecord{}, fmt.Errorf("read the lobby: %w", err)
		}
		if len(rooms) > 1 {
			return postgres.RoomRecord{}, fmt.Errorf("join code %s is used by lobbies of several games; pass game_key too", input.JoinCode)
		}
	default:
		return postgres.RoomRecord{}, errors.New("pass either lobby_id or join_code")
	}
	if len(rooms) == 0 {
		return postgres.RoomRecord{}, errors.New("no such lobby; retention may have deleted it")
	}
	return rooms[0], nil
}

// retentionDeadline is when default retention deletes room at the latest:
// after its idle period, which a member's connection restarts, or at its
// maximum age, whichever comes first.
func (reader lobbyReader) retentionDeadline(room postgres.RoomRecord) time.Time {
	retention := reader.lobbyRetention(room.GameKey)
	idle := room.LastConnectedAt.Add(retention.IdlePeriod)
	aged := room.CreatedAt.Add(retention.MaximumAge)
	if aged.Before(idle) {
		return aged.UTC()
	}
	return idle.UTC()
}

// latestLeaseExpiry is when the member's latest unreleased lease of its
// current generation expires.
func latestLeaseExpiry(member postgres.MembershipRecord, leases []postgres.LeaseRecord) *time.Time {
	var latest *time.Time
	for _, lease := range leases {
		if lease.PlayerID != member.PlayerID || lease.Generation != member.Generation || lease.ReleasedAt != nil {
			continue
		}
		if latest == nil || lease.ExpiresAt.After(*latest) {
			expiresAt := lease.ExpiresAt.UTC()
			latest = &expiresAt
		}
	}
	return latest
}

func lookUp(names map[string]string, id *string) *string {
	if id == nil {
		return nil
	}
	name, found := names[*id]
	if !found {
		return nil
	}
	return &name
}

func utcPointer(instant *time.Time) *time.Time {
	if instant == nil {
		return nil
	}
	utc := instant.UTC()
	return &utc
}

func anySlice[T any](values []T) []any {
	converted := make([]any, len(values))
	for index, value := range values {
		converted[index] = value
	}
	return converted
}
