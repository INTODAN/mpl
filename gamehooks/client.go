package gamehooks

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"islandBattles/lobby/domain"
)

// Client calls one game's hooks, at its hooks URL, for the lobby. Each call
// is signed with the game's shared secret and has DefaultTimeout. A
// transport error, a timeout, a 5xx answer or any answer the protocol doesn't
// allow is reported as unavailable, which the player gets as 503; a game's
// 422 or 409 refusal becomes the matching lobby error.
type Client struct {
	key       string
	transport *transport
}

// NewClient returns the client of the game with key, whose hooks are served
// under hooksURL (see ParseServiceURL), signed with secret, which has at
// least MinSecretBytes bytes.
func NewClient(key, hooksURL string, secret []byte, options ...Option) (*Client, error) {
	if err := domain.ValidateGameKey(key); err != nil {
		return nil, errors.New("invalid game key")
	}
	transport, err := newTransport(hooksURL, secret, options)
	if err != nil {
		return nil, err
	}
	return &Client{key: key, transport: transport}, nil
}

// Registration declares the game to the lobby policy with hooks that call
// the game, and the start rules and retention that the configuration fixes.
func (client *Client) Registration(rules domain.StartRules, retention domain.LobbyRetention) domain.GameRegistration {
	return domain.GameRegistration{
		Key:                    client.key,
		ValidateSettings:       client.ValidateSettings,
		StartRules:             rules,
		Retention:              retention,
		ReadyGate:              client.ReadyGate,
		ValidateSeat:           client.ValidateSeat,
		ValidateMemberSettings: client.ValidateMemberSettings,
		GameOver:               client.RoundEnded,
		StartGame:              client.Start,
	}
}

// refusal is a game's 422: a message for the player, which the lobby reports
// with the code of what it checked.
type refusal struct{ detail string }

func (err *refusal) Error() string { return err.detail }

// validate calls a hook that answers 204 or 200 when it accepts, and 422 to
// refuse.
func (client *Client) validate(ctx context.Context, hook string, request, into any) error {
	answer, err := client.transport.send(ctx, http.MethodPost, []string{hook}, request)
	if err != nil {
		return err
	}
	switch {
	case answer.status == http.StatusNoContent && into == nil:
		return nil
	case answer.status == http.StatusOK && into != nil:
		return decode(hook, answer, into)
	case answer.status == http.StatusUnprocessableEntity:
		detail := boundedDetail(answer.problem().Detail)
		if detail == "" {
			detail = "the game refused these settings"
		}
		return &refusal{detail: detail}
	default:
		return unexpected(hook, answer)
	}
}

// ValidateSettings calls validate-settings; it is a
// domain.GameSettingsValidator.
func (client *Client) ValidateSettings(ctx context.Context, lobbyID string, settings json.RawMessage, teams []domain.TeamDefinition) error {
	return client.validate(ctx, HookValidateSettings, ValidateSettingsRequest{LobbyID: lobbyID, Settings: settings, Teams: teams}, nil)
}

// ValidateSeat calls validate-seat; it is a domain.SeatValidator.
func (client *Client) ValidateSeat(ctx context.Context, lobbyID string, settings json.RawMessage) (string, error) {
	var seat ValidateSeatResponse
	if err := client.validate(ctx, HookValidateSeat, ValidateSeatRequest{LobbyID: lobbyID, Settings: settings}, &seat); err != nil {
		return "", err
	}
	return seat.DisplayName, nil
}

// ValidateMemberSettings calls validate-member-settings; it is a
// domain.MemberSettingsValidator.
func (client *Client) ValidateMemberSettings(ctx context.Context, lobbyID string, settings json.RawMessage) error {
	return client.validate(ctx, HookValidateMemberSettings, ValidateMemberSettingsRequest{LobbyID: lobbyID, Settings: settings}, nil)
}

// ReadyGate is a domain.ReadyGate whose check calls ready-check. The lobby
// runs the check inside the transaction of a ready or start request, so the
// call holds the room's lock for as long as it takes, at most the timeout,
// and the game should answer it quickly, from the request alone.
func (client *Client) ReadyGate(ctx context.Context, lobbyID string) (domain.ReadyCheck, error) {
	return func(members []domain.ReadyMember, atStart bool) error {
		return client.ReadyCheck(ctx, lobbyID, members, atStart)
	}, nil
}

// ReadyCheck calls ready-check. A 409 with the problem code
// game_requirement is the game's refusal, whose type and detail reach the
// player unchanged.
func (client *Client) ReadyCheck(ctx context.Context, lobbyID string, members []domain.ReadyMember, atStart bool) error {
	request := ReadyCheckRequest{LobbyID: lobbyID, Members: make([]ReadyMember, 0, len(members)), AtStart: atStart}
	for _, member := range members {
		request.Members = append(request.Members, ReadyMember{PlayerID: member.PlayerID, DisplayName: member.DisplayName, Settings: member.Settings})
	}
	answer, err := client.transport.send(ctx, http.MethodPost, []string{HookReadyCheck}, request)
	if err != nil {
		return err
	}
	switch answer.status {
	case http.StatusNoContent:
		return nil
	case http.StatusConflict:
		problem := answer.problem()
		if problem.Code == string(domain.CodeGameRequirement) && problem.Type != "" && problem.Detail != "" {
			return domain.GameRequirement(problem.Type, boundedDetail(problem.Detail))
		}
	}
	return unexpected(HookReadyCheck, answer)
}

// Start calls start; it is a domain.GameStarter.
func (client *Client) Start(ctx context.Context, snapshot domain.Snapshot) (string, error) {
	answer, err := client.transport.send(ctx, http.MethodPost, []string{HookStart}, StartRequest{Snapshot: snapshot})
	if err != nil {
		return "", err
	}
	if answer.status != http.StatusOK {
		return "", unexpected(HookStart, answer)
	}
	var started StartResponse
	if err := decode(HookStart, answer, &started); err != nil {
		return "", err
	}
	if started.GameID == "" {
		return "", unavailable(errors.New("start answered without a game_id"))
	}
	return started.GameID, nil
}

// RoundEnded calls round-ended; it is a domain.GameOverCheck.
func (client *Client) RoundEnded(ctx context.Context, lobbyID string, round int64) (bool, error) {
	answer, err := client.transport.send(ctx, http.MethodPost, []string{HookRoundEnded}, RoundEndedRequest{LobbyID: lobbyID, Round: round})
	if err != nil {
		return false, err
	}
	if answer.status != http.StatusOK {
		return false, unexpected(HookRoundEnded, answer)
	}
	var ended RoundEndedResponse
	if err := decode(HookRoundEnded, answer, &ended); err != nil {
		return false, err
	}
	return ended.Ended, nil
}
