package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"islandBattles/lobby/domain"
)

type CreateSessionInput struct {
	Body CreateSessionBody `json:"-"`
}
type CreateSessionBody struct {
	DisplayName string `json:"display_name" minLength:"1" maxLength:"40"`
}
type CreateSessionOutput struct {
	Body GuestSession `json:"-"`
}
type GuestSession struct {
	PlayerID    string    `json:"player_id" format:"uuid"`
	AccessToken string    `json:"access_token"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type ConnectionTokenInput struct {
	Body struct{} `json:"-"`
}
type ConnectionTokenOutput struct {
	Body ConnectionToken `json:"-"`
}
type ConnectionToken struct {
	Token        string    `json:"token"`
	ExpiresAt    time.Time `json:"expires_at"`
	WebsocketURL string    `json:"websocket_url" format:"uri"`
}

// TeamDefinition names a team of a new lobby. A team holds one member.
type TeamDefinition struct {
	Name string `json:"name" minLength:"1" maxLength:"40"`
}
type CreateLobbyInput struct {
	Body CreateLobbyBody `json:"-"`
}
type CreateLobbyBody struct {
	RequestID        string           `json:"request_id" format:"uuid"`
	GameKey          string           `json:"game_key" pattern:"^[a-z][a-z0-9-]{0,31}$"`
	Name             string           `json:"name" minLength:"1" maxLength:"80"`
	Teams            []TeamDefinition `json:"teams" minItems:"2" maxItems:"16" doc:"The lobby's teams, in order. Each holds one member, a person or a seat, so the lobby holds as many members as it has teams."`
	ClientInstanceID string           `json:"client_instance_id" format:"uuid"`
	GameSettings     GameSettings     `json:"game_settings,omitempty"`
}

// GameSettings is a game's own JSON object. It keeps the raw bytes, so numbers
// and nested values reach the domain without a lossy decode.
type GameSettings json.RawMessage

func (settings GameSettings) MarshalJSON() ([]byte, error) {
	if len(settings) == 0 {
		return []byte(domain.EmptyGameSettings), nil
	}
	return settings, nil
}
func (settings *GameSettings) UnmarshalJSON(data []byte) error {
	*settings = bytes.Clone(data)
	return nil
}
func (GameSettings) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Type: huma.TypeObject, AdditionalProperties: true, Description: "Settings for the game, as a JSON object that only the game reads. The game checks them when the lobby is created, and again when the host changes them while the lobby waits. The lobby returns them compacted with sorted keys, and omitted settings as an empty object."}
}

type JoinLobbyInput struct {
	Body JoinLobbyBody `json:"-"`
}
type JoinLobbyBody struct {
	GameKey          string `json:"game_key" pattern:"^[a-z][a-z0-9-]{0,31}$"`
	JoinCode         string `json:"join_code" minLength:"8" maxLength:"8"`
	ClientInstanceID string `json:"client_instance_id" format:"uuid"`
}
type WatchLobbyInput struct {
	Body WatchLobbyBody `json:"-"`
}
type WatchLobbyBody struct {
	GameKey  string `json:"game_key" pattern:"^[a-z][a-z0-9-]{0,31}$"`
	JoinCode string `json:"join_code" minLength:"8" maxLength:"8"`
}
type WatchLobbyOutput struct {
	Body WatchAccess `json:"-"`
}

// WatchAccess tells a person who asked to watch where the lobby's game
// arrives for watchers.
type WatchAccess struct {
	GameKey string `json:"game_key"`
	LobbyID string `json:"lobby_id" format:"uuid"`
	Channel string `json:"channel" doc:"The lobby's watch channel, watch:<game_key>:<lobby_id>, where its game publishes what watchers may see. The subscribe proxy allows it while you are a watcher the host hasn't kicked, or an active member."`
	Member  bool   `json:"member" doc:"True when you are an active member of the lobby. You get no watcher row and keep your member access, which includes the watch channel."`
}
type WatchersOutput struct {
	Body WatcherList `json:"-"`
}
type WatcherList struct {
	Watchers []Watcher `json:"watchers" nullable:"false" doc:"The watchers the host hasn't kicked, in the order they started watching."`
}
type Watcher struct {
	PlayerID    string    `json:"player_id" format:"uuid" doc:"The watcher's player ID, for kicking them."`
	DisplayName string    `json:"display_name"`
	JoinedAt    time.Time `json:"joined_at" doc:"When they started watching."`
}
type KickWatcherInput struct {
	ScopeInput
	PlayerID string `path:"player_id" format:"uuid" doc:"The watcher's player ID."`
}
type ScopeInput struct {
	GameKey string `path:"game_key" pattern:"^[a-z][a-z0-9-]{0,31}$"`
	LobbyID string `path:"lobby_id" format:"uuid"`
}
type CreateLobbyOutput struct {
	Body Snapshot `json:"-"`
}
type JoinLobbyOutput struct {
	Body Snapshot `json:"-"`
}
type SnapshotOutput struct {
	Body Snapshot `json:"-"`
}

// NullableUUID distinguishes JSON null (clear) from a valid UUID. Omission is
// rejected by the containing field's `required` schema tag before unmarshalling.
type NullableUUID struct{ Value *string }

func (value *NullableUUID) UnmarshalJSON(data []byte) error {
	if bytes.Equal(data, []byte("null")) {
		value.Value = nil
		return nil
	}
	var parsed string
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}
	if parsed == "" {
		return fmt.Errorf("team_id must be a UUID or null")
	}
	value.Value = &parsed
	return nil
}
func (NullableUUID) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Type: "string", Format: "uuid", Nullable: true}
}

type SelectTeamInput struct {
	ScopeInput
	Body SelectTeamBody `json:"-"`
}
type SelectTeamBody struct {
	TeamID NullableUUID `json:"team_id" required:"true"`
}
type ReadyInput struct {
	ScopeInput
	Body ReadyBody `json:"-"`
}
type ReadyBody struct {
	Ready bool `json:"ready" required:"true"`
}
type StartInput struct {
	ScopeInput
	Body struct{} `json:"-"`
}
type ChangeSettingsInput struct {
	ScopeInput
	Body ChangeSettingsBody `json:"-"`
}
type ChangeSettingsBody struct {
	Teams        []TeamDefinition `json:"teams" minItems:"2" maxItems:"16" doc:"The lobby's new teams, in order, with the rules of a create request. They replace the lobby's teams by position: the first keeps the lobby's first team and its ID and takes this name, and so on; teams beyond the lobby's are added, and the lobby's teams beyond these are removed."`
	GameSettings GameSettings     `json:"game_settings,omitempty"`
}
type AddSeatInput struct {
	ScopeInput
	Body AddSeatBody `json:"-"`
}
type AddSeatBody struct {
	RequestID    string       `json:"request_id,omitempty" format:"uuid" doc:"Optional. Repeating a request ID with the same team and settings returns the current snapshot without a second seat; with another team or settings it fails with idempotency_conflict."`
	TeamID       string       `json:"team_id" format:"uuid" doc:"The team the seat joins."`
	SeatSettings SeatSettings `json:"seat_settings" required:"true"`
}

// SeatSettings is a game's own JSON object for a seat that no person plays.
// Like GameSettings, it keeps the raw bytes.
type SeatSettings json.RawMessage

func (settings SeatSettings) MarshalJSON() ([]byte, error) {
	if len(settings) == 0 {
		return []byte("null"), nil
	}
	return settings, nil
}
func (settings *SeatSettings) UnmarshalJSON(data []byte) error {
	*settings = bytes.Clone(data)
	return nil
}
func (SeatSettings) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Type: huma.TypeObject, AdditionalProperties: true, Description: "Settings for a seat that no person plays, as a JSON object that only the game reads, such as the level of the game's bot that plays the seat. The game checks them when the host adds the seat; invalid settings fail with invalid_seat_settings."}
}

// MemberSeatSettings is a member's seat settings: the object for a seat, and
// null for a person.
type MemberSeatSettings json.RawMessage

func (settings MemberSeatSettings) MarshalJSON() ([]byte, error) {
	if len(settings) == 0 {
		return []byte("null"), nil
	}
	return settings, nil
}
func (settings *MemberSeatSettings) UnmarshalJSON(data []byte) error {
	*settings = bytes.Clone(data)
	return nil
}
func (MemberSeatSettings) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Type: huma.TypeObject, AdditionalProperties: true, Nullable: true, Description: "For a seat, the settings the host added it with, compacted with sorted keys, which only the game reads. Null for a person."}
}

type RemoveSeatInput struct {
	ScopeInput
	SeatID string `path:"seat_id" format:"uuid" doc:"The seat's member ID."`
}
type LeaveInput struct{ ScopeInput }
type EmptyOutput struct{}
type HeartbeatInput struct {
	ScopeInput
	Body HeartbeatBody `json:"-"`
}
type HeartbeatBody struct {
	ClientInstanceID     string `json:"client_instance_id" format:"uuid"`
	MembershipGeneration int64  `json:"membership_generation" minimum:"1"`
}
type HeartbeatOutput struct {
	Body HeartbeatResult `json:"-"`
}
type HeartbeatResult struct {
	Version              int64      `json:"version"`
	MembershipGeneration int64      `json:"membership_generation"`
	LeaseExpiresAt       time.Time  `json:"lease_expires_at"`
	DisconnectedUntil    *time.Time `json:"disconnected_until"`
}
type ReleaseLeaseInput struct {
	ScopeInput
	ClientInstanceID     string `path:"client_instance_id" format:"uuid"`
	MembershipGeneration int64  `query:"generation" minimum:"1" required:"true"`
}

type Snapshot struct {
	Lobby                Lobby              `json:"lobby"`
	Teams                []Team             `json:"teams"`
	Members              []Member           `json:"members"`
	StartRoster          []StartRosterEntry `json:"start_roster"`
	MembershipGeneration int64              `json:"membership_generation"`
	Channel              string             `json:"channel"`
	StartRules           StartRules         `json:"start_rules"`
	WatcherCount         int                `json:"watcher_count" minimum:"0" doc:"How many people watch the lobby with its join code, kicked watchers left out. Watchers take no seat. A change to it is a lobby change, announced with lobby.changed."`
}

// StartRules are the start conditions the lobby's game declares on top of the
// default ones, so a client can show why the lobby can't start yet.
type StartRules struct {
	EveryTeamNeedsMember bool `json:"every_team_needs_member" doc:"Starting fails with empty_teams while a team has no member."`
}
type Lobby struct {
	ID           string       `json:"id"`
	GameKey      string       `json:"game_key"`
	Name         string       `json:"name"`
	JoinCode     string       `json:"join_code"`
	Capacity     int          `json:"capacity" doc:"The number of members the lobby holds, which is its number of teams, since each team holds one member."`
	Phase        domain.Phase `json:"phase"`
	Version      int64        `json:"version"`
	HostPlayerID *string      `json:"host_player_id"`
	CreatedAt    time.Time    `json:"created_at"`
	GameSettings GameSettings `json:"game_settings"`
}

// Team is one of a lobby's teams. It holds at most one member.
type Team struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Order int    `json:"order"`
}
type Member struct {
	ID                string             `json:"id"`
	PlayerID          string             `json:"player_id"`
	DisplayName       string             `json:"display_name"`
	Generation        int64              `json:"generation"`
	Active            bool               `json:"active"`
	TeamID            *string            `json:"team_id"`
	Ready             bool               `json:"ready"`
	JoinedAt          time.Time          `json:"joined_at"`
	JoinSequence      int64              `json:"join_sequence"`
	DisconnectedUntil *time.Time         `json:"disconnected_until"`
	Kind              domain.MemberKind  `json:"kind" enum:"human,seat" doc:"human: a person who joined. seat: a seat that no person plays, such as the game's bot, which the host added to a team; it is always ready, never disconnects or hosts, and the game plays it from seat_settings. Its id is the seat ID for removing it."`
	SeatSettings      MemberSeatSettings `json:"seat_settings"`
}
type StartRosterEntry struct {
	PlayerID  string `json:"player_id"`
	TeamID    string `json:"team_id"`
	MemberID  string `json:"member_id"`
	TeamOrder int    `json:"team_order"`
}
type LobbyInvalidation struct {
	SchemaVersion int    `json:"schema_version" enum:"1"`
	EventID       string `json:"event_id"`
	GameKey       string `json:"game_key"`
	LobbyID       string `json:"lobby_id"`
	Version       int64  `json:"version" minimum:"1"`
	Type          string `json:"type" enum:"lobby.changed"`
}

func snapshotFromDomain(source domain.Snapshot) Snapshot {
	teams := make([]Team, len(source.Teams))
	for index, team := range source.Teams {
		teams[index] = Team{ID: team.ID, Name: team.Name, Order: team.Order}
	}
	members := make([]Member, len(source.Members))
	for index, member := range source.Members {
		kind, settings := domain.MemberHuman, MemberSeatSettings(nil)
		if member.IsSeat() {
			kind, settings = domain.MemberSeat, MemberSeatSettings(member.SeatSettings)
		}
		members[index] = Member{ID: member.ID, PlayerID: member.PlayerID, DisplayName: member.DisplayName, Generation: member.Generation, Active: member.Active, TeamID: member.TeamID, Ready: member.Ready, JoinedAt: member.JoinedAt, JoinSequence: member.JoinSequence, DisconnectedUntil: member.DisconnectedUntil, Kind: kind, SeatSettings: settings}
	}
	roster := make([]StartRosterEntry, len(source.StartRoster))
	for index, entry := range source.StartRoster {
		roster[index] = StartRosterEntry{PlayerID: entry.PlayerID, TeamID: entry.TeamID, MemberID: entry.MemberID, TeamOrder: entry.TeamOrder}
	}
	room := source.Lobby
	return Snapshot{Lobby: Lobby{ID: room.ID, GameKey: room.GameKey, Name: room.Name, JoinCode: room.JoinCode, Capacity: room.Capacity, Phase: room.Phase, Version: room.Version, HostPlayerID: room.HostPlayerID, CreatedAt: room.CreatedAt, GameSettings: GameSettings(room.GameSettings)}, Teams: teams, Members: members, StartRoster: roster, MembershipGeneration: source.MembershipGeneration, Channel: source.Channel, StartRules: StartRules{EveryTeamNeedsMember: source.StartRules.EveryTeamNeedsMember}, WatcherCount: source.WatcherCount}
}
