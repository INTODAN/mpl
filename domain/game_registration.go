package domain

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
)

// GameRegistration declares one game that may use the lobby. Every field is a
// declaration the lobby applies without knowing the game, and a zero field
// keeps the lobby's default behavior, so new options are added as new fields.
type GameRegistration struct {
	// Key is the canonical game key used in requests, URLs and channels.
	Key string
	// ValidateSettings checks the game settings of a create request. Nil means
	// the game takes no settings and accepts only an empty object.
	ValidateSettings GameSettingsValidator
	// StartRules adds start conditions to the lobby's default ones.
	StartRules StartRules
	// Retention overrides when the game's lobbies are deleted. Each zero
	// period keeps the policy's default.
	Retention LobbyRetention
	// ReadyGate lets the game keep members from getting ready, and a lobby
	// from starting, until they meet a condition of the game's own. Nil adds
	// no condition.
	ReadyGate ReadyGate
	// ValidateSeat checks the seat settings of a seat that the host adds to a
	// team, such as the level of the game's bot that plays it, and names the
	// seat. Nil means the game has no seats and refuses every one.
	ValidateSeat SeatValidator
}

// SeatValidator receives the settings of a seat that the host adds, as a
// canonical JSON object, and returns the display name that the lobby shows
// for the seat, such as "Bot (level 2)". To reject the settings, return an
// error whose message is safe to show the client. To report that the check
// itself failed, return the context's error or an error wrapping
// ErrUnavailable.
type SeatValidator func(ctx context.Context, settings json.RawMessage) (displayName string, err error)

// ReadyGate loads what a game needs to decide which members of a lobby may get
// ready, such as a choice each player makes in the game's own API. The lobby
// calls it with the lobby's ID before the transaction of a request that sets a
// member ready or starts the lobby, so the game never reads its data while the
// lobby holds the room's lock. The returned ReadyCheck then runs inside the
// transaction, for the member getting ready or for every member of the start
// roster. Because the data is read first, a member must keep meeting the
// condition once they do, for as long as the lobby is waiting: a game may let
// a member change what they chose, but not take the choice away.
//
// To report that the gate itself could not run, return the context's error or
// an error wrapping ErrUnavailable; any other error is reported as unavailable
// too.
type ReadyGate func(ctx context.Context, lobbyID string) (ReadyCheck, error)

// ReadyMember is a member that a ReadyCheck decides about.
type ReadyMember struct {
	PlayerID    string
	DisplayName string
}

// ReadyCheck reports whether members may be ready. It gets the one member who
// is getting ready, with atStart false, or the whole start roster in the
// lobby's member order, with atStart true, so a refusal at the start can name
// the members who must act, who are not the host who sees it. It returns nil,
// or an error from GameRequirement that names the game's problem type and
// says what to do. Any other error is reported as unavailable.
type ReadyCheck func(members []ReadyMember, atStart bool) error

// CheckReady loads the game's ready gate for a lobby, or returns nil when the
// game declares none. Its errors are reported as unavailable.
func (game GameRegistration) CheckReady(ctx context.Context, lobbyID string) (ReadyCheck, error) {
	if game.ReadyGate == nil {
		return nil, nil
	}
	check, err := game.ReadyGate(ctx, lobbyID)
	if err != nil {
		return nil, gateUnavailable(err)
	}
	if check == nil {
		return nil, gateUnavailable(errors.New("the game's ready gate returned no check"))
	}
	return func(members []ReadyMember, atStart bool) error {
		err := check(members, atStart)
		if err == nil || ErrorCodeOf(err) == CodeGameRequirement {
			return err
		}
		return gateUnavailable(err)
	}, nil
}

// gateUnavailable reports a ready gate that could not decide.
func gateUnavailable(cause error) error {
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) || ErrorCodeOf(cause) == CodeUnavailable {
		return cause
	}
	return &Error{Code: CodeUnavailable, Message: ErrUnavailable.Message, Cause: cause}
}

// StartRules are conditions a game declares for starting its lobbies, on top
// of the default that at least two members are ready and every active member
// has chosen a team. The zero value adds none. Snapshots carry them, so
// clients can explain why a lobby can't start yet.
type StartRules struct {
	// EveryTeamNeedsMember refuses to start while a team has no member.
	EveryTeamNeedsMember bool `json:"every_team_needs_member"`
}

// Check reports the first rule that the frozen roster breaks. An empty team
// fails with CodeEmptyTeams, whose TeamIDs list the empty teams in order.
func (rules StartRules) Check(teams []Team, roster StartRoster) error {
	if !rules.EveryTeamNeedsMember {
		return nil
	}
	filled := make(map[string]bool, len(roster))
	for _, entry := range roster {
		filled[entry.TeamID] = true
	}
	ordered := slices.Clone(teams)
	slices.SortFunc(ordered, func(first, second Team) int {
		return cmp.Or(cmp.Compare(first.Order, second.Order), strings.Compare(first.ID, second.ID))
	})
	var ids, names []string
	for _, team := range ordered {
		if !filled[team.ID] {
			ids = append(ids, team.ID)
			names = append(names, team.Name)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return &Error{Code: CodeEmptyTeams, Message: "every team needs a player before the lobby can start; empty: " + strings.Join(names, ", "), TeamIDs: ids}
}

// GameSettingsValidator receives the settings of a create request as a
// canonical JSON object, `{}` when the request had none, with the request's
// validated teams in order, so settings can be checked against the lobby they
// configure. To reject them, return an error whose message is safe to show the
// client. To report that the check itself failed, return the context's error or
// an error wrapping ErrUnavailable.
type GameSettingsValidator func(ctx context.Context, settings json.RawMessage, teams []TeamDefinition) error

// EmptyGameSettings is stored for a lobby created without game settings.
const EmptyGameSettings = "{}"

// MaxGameSettingsBytes bounds the canonical settings stored with each lobby.
const MaxGameSettingsBytes = 16 * 1024

// NormalizeGameSettings returns settings as one compact JSON object with
// sorted keys, so requests that differ only in formatting or key order match.
// Omitted settings become an empty object. The lobby never reads the values.
func NormalizeGameSettings(settings json.RawMessage) (json.RawMessage, error) {
	if len(settings) == 0 {
		return json.RawMessage(EmptyGameSettings), nil
	}
	return normalizeObject(settings, CodeInvalidGameSettings, "game settings", MaxGameSettingsBytes)
}

// MaxSeatSettingsBytes bounds the canonical settings stored with each seat.
const MaxSeatSettingsBytes = 4 * 1024

// NormalizeSeatSettings returns a seat's settings as one compact JSON object
// with sorted keys. A seat needs settings, so omitted ones are refused.
func NormalizeSeatSettings(settings json.RawMessage) (json.RawMessage, error) {
	if len(settings) == 0 {
		return nil, NewError(CodeInvalidSeatSettings, "seat settings must be a JSON object")
	}
	return normalizeObject(settings, CodeInvalidSeatSettings, "seat settings", MaxSeatSettingsBytes)
}

// normalizeObject canonicalizes one JSON object of at most maximum bytes, or
// fails with code and a message that names what the object is.
func normalizeObject(settings json.RawMessage, code ErrorCode, what string, maximum int) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(settings))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, NewError(code, what+" must be a JSON object")
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, NewError(code, what+" must be a single JSON object")
	}
	if containsNUL(object) {
		// PostgreSQL text and jsonb can't hold NUL, and snapshots are stored as jsonb.
		return nil, NewError(code, what+" must not contain NUL characters")
	}
	var canonical bytes.Buffer
	encoder := json.NewEncoder(&canonical)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(object); err != nil {
		return nil, NewError(code, what+" must be a JSON object")
	}
	result := bytes.TrimSuffix(canonical.Bytes(), []byte("\n"))
	if len(result) > maximum {
		return nil, NewError(code, what+" are too large")
	}
	return result, nil
}

// containsNUL reports whether any key or string in a decoded value holds NUL.
func containsNUL(value any) bool {
	switch typed := value.(type) {
	case string:
		return strings.ContainsRune(typed, 0)
	case map[string]any:
		for key, item := range typed {
			if strings.ContainsRune(key, 0) || containsNUL(item) {
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if containsNUL(item) {
				return true
			}
		}
	}
	return false
}

// ValidateGameSettings normalizes settings and checks them with the game's
// validator against the lobby's validated teams. It returns the canonical
// settings to store with the lobby.
func (policy Policy) ValidateGameSettings(ctx context.Context, gameKey string, settings json.RawMessage, teams []TeamDefinition) (json.RawMessage, error) {
	game, err := policy.Game(gameKey)
	if err != nil {
		return nil, err
	}
	canonical, err := NormalizeGameSettings(settings)
	if err != nil {
		return nil, err
	}
	if game.ValidateSettings == nil {
		if string(canonical) != EmptyGameSettings {
			return nil, NewError(CodeInvalidGameSettings, "this game takes no settings")
		}
		return canonical, nil
	}
	err = game.ValidateSettings(ctx, bytes.Clone(canonical), slices.Clone(teams))
	switch {
	case err == nil:
		return canonical, nil
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded), errors.Is(err, ErrUnavailable):
		return nil, err
	default:
		return nil, &Error{Code: CodeInvalidGameSettings, Message: err.Error(), Cause: err}
	}
}

// ValidateSeatSettings normalizes a seat's settings and checks them with the
// game's seat validator. It returns the canonical settings to store with the
// seat and the seat's display name. A game without a seat validator has no
// seats and refuses every one with CodeInvalidSeatSettings.
func (policy Policy) ValidateSeatSettings(ctx context.Context, gameKey string, settings json.RawMessage) (json.RawMessage, string, error) {
	game, err := policy.Game(gameKey)
	if err != nil {
		return nil, "", err
	}
	canonical, err := NormalizeSeatSettings(settings)
	if err != nil {
		return nil, "", err
	}
	if game.ValidateSeat == nil {
		return nil, "", NewError(CodeInvalidSeatSettings, "this game has no seats that the host can add")
	}
	name, err := game.ValidateSeat(ctx, bytes.Clone(canonical))
	switch {
	case err == nil:
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded), errors.Is(err, ErrUnavailable):
		return nil, "", err
	default:
		return nil, "", &Error{Code: CodeInvalidSeatSettings, Message: err.Error(), Cause: err}
	}
	name, err = NormalizeName(name, 40)
	if err != nil {
		return nil, "", &Error{Code: CodeUnavailable, Message: ErrUnavailable.Message, Cause: errors.New("the game named a seat with an invalid display name")}
	}
	return canonical, name, nil
}
