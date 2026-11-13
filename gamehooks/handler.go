package gamehooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"time"

	"islandBattles/lobby/domain"
)

// HandlerOption changes a hooks handler.
type HandlerOption func(*hooksHandler)

// WithHandlerClock replaces the clock that signatures are checked against.
func WithHandlerClock(now func() time.Time) HandlerOption {
	return func(handler *hooksHandler) { handler.now = now }
}

// NewHandler serves a game's hooks from the Go functions of its
// registration, so a game written in Go mounts it at its hooks URL. Each
// request is POST .../<hook>, and the last element of the path names the
// hook, so the handler can be mounted under any prefix, with or without
// http.StripPrefix. It refuses a request whose Lobby-Signature doesn't verify
// with 401, before it reads anything else.
//
// The registration's functions answer as they do in the lobby's own process.
// A validator's error is a 422 whose detail is the error's message, and a
// ReadyCheck's domain.GameRequirement a 409 game_requirement; an error that
// wraps domain.ErrUnavailable, or the context's, is a 503. A hook whose
// function is nil answers as the lobby treats a nil field: validate-settings
// accepts only `{}`, validate-seat and validate-member-settings refuse every
// value, ready-check accepts everyone, and round-ended answers false, so the
// lobby is never reopened. start needs StartGame, and answers 503 without it.
// The registration's Key, StartRules and Retention aren't used: the lobby's
// configuration holds them.
func NewHandler(secret []byte, game domain.GameRegistration, options ...HandlerOption) (http.Handler, error) {
	if len(secret) < MinSecretBytes {
		return nil, errors.New("the shared secret needs at least 32 bytes")
	}
	handler := &hooksHandler{secret: bytes.Clone(secret), game: game, now: time.Now}
	for _, option := range options {
		option(handler)
	}
	if handler.now == nil {
		return nil, errors.New("invalid game hooks handler option")
	}
	return handler, nil
}

type hooksHandler struct {
	secret []byte
	game   domain.GameRegistration
	now    func() time.Time
}

// SignedPath is the path that a request's signature covers: the path of the
// request as the client sent it, before any http.StripPrefix.
func SignedPath(request *http.Request) string {
	if request.RequestURI != "" {
		if target, err := url.ParseRequestURI(request.RequestURI); err == nil {
			return target.EscapedPath()
		}
	}
	return request.URL.EscapedPath()
}

// ReadSigned reads a request's body, at most MaxBodyBytes, and checks its
// Lobby-Signature with secret. It fails with ErrBadSignature, or with an
// error reading the body.
func ReadSigned(request *http.Request, secret []byte, now time.Time) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(request.Body, MaxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > MaxBodyBytes {
		return nil, errors.New("the request body is too large")
	}
	if err := Verify(secret, request.Header.Get(SignatureHeader), now, request.Method, SignedPath(request), body); err != nil {
		return nil, err
	}
	return body, nil
}

func (handler *hooksHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	body, err := ReadSigned(request, handler.secret, handler.now())
	switch {
	case errors.Is(err, ErrBadSignature):
		WriteProblem(response, Problem{Status: http.StatusUnauthorized, Detail: err.Error(), Code: string(domain.CodeUnauthenticated)})
		return
	case err != nil:
		WriteProblem(response, Problem{Status: http.StatusBadRequest, Detail: "the request body can't be read", Code: string(domain.CodeInvalidInput)})
		return
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		WriteProblem(response, Problem{Status: http.StatusMethodNotAllowed, Detail: "hooks are called with POST"})
		return
	}
	ctx := request.Context()
	switch hook := path.Base(request.URL.Path); hook {
	case HookValidateSettings:
		var input ValidateSettingsRequest
		if decodeHook(response, body, &input) {
			handler.validateSettings(ctx, response, input)
		}
	case HookValidateSeat:
		var input ValidateSeatRequest
		if decodeHook(response, body, &input) {
			handler.validateSeat(ctx, response, input)
		}
	case HookValidateMemberSettings:
		var input ValidateMemberSettingsRequest
		if decodeHook(response, body, &input) {
			handler.validateMemberSettings(ctx, response, input)
		}
	case HookReadyCheck:
		var input ReadyCheckRequest
		if decodeHook(response, body, &input) {
			handler.readyCheck(ctx, response, input)
		}
	case HookStart:
		var input StartRequest
		if decodeHook(response, body, &input) {
			normalizeSnapshot(&input.Snapshot)
			handler.start(ctx, response, input)
		}
	case HookRoundEnded:
		var input RoundEndedRequest
		if decodeHook(response, body, &input) {
			handler.roundEnded(ctx, response, input)
		}
	default:
		WriteProblem(response, Problem{Status: http.StatusNotFound, Detail: "no such hook", Code: string(domain.CodeNotFound)})
	}
}

func decodeHook(response http.ResponseWriter, body []byte, into any) bool {
	if err := json.Unmarshal(body, into); err != nil {
		WriteProblem(response, Problem{Status: http.StatusBadRequest, Detail: "the request body is not the hook's JSON", Code: string(domain.CodeInvalidInput)})
		return false
	}
	return true
}

// refuse answers a validator's error: 503 when the check itself failed, and
// otherwise a 422 whose detail is the error's message.
func refuse(response http.ResponseWriter, err error) {
	if failedToCheck(err) {
		WriteProblem(response, Problem{Status: http.StatusServiceUnavailable, Detail: domain.ErrUnavailable.Message, Code: string(domain.CodeUnavailable)})
		return
	}
	WriteProblem(response, Problem{Status: http.StatusUnprocessableEntity, Detail: err.Error()})
}

func failedToCheck(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, domain.ErrUnavailable)
}

func unavailableProblem(response http.ResponseWriter) {
	WriteProblem(response, Problem{Status: http.StatusServiceUnavailable, Detail: domain.ErrUnavailable.Message, Code: string(domain.CodeUnavailable)})
}

func (handler *hooksHandler) validateSettings(ctx context.Context, response http.ResponseWriter, input ValidateSettingsRequest) {
	if handler.game.ValidateSettings == nil {
		if string(input.Settings) != domain.EmptyGameSettings {
			refuse(response, errors.New("this game takes no settings"))
			return
		}
		response.WriteHeader(http.StatusNoContent)
		return
	}
	if err := handler.game.ValidateSettings(ctx, input.LobbyID, input.Settings, input.Teams); err != nil {
		refuse(response, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (handler *hooksHandler) validateSeat(ctx context.Context, response http.ResponseWriter, input ValidateSeatRequest) {
	if handler.game.ValidateSeat == nil {
		refuse(response, errors.New("this game has no seats that the host can add"))
		return
	}
	name, err := handler.game.ValidateSeat(ctx, input.LobbyID, input.Settings)
	if err != nil {
		refuse(response, err)
		return
	}
	writeJSON(response, http.StatusOK, ValidateSeatResponse{DisplayName: name})
}

func (handler *hooksHandler) validateMemberSettings(ctx context.Context, response http.ResponseWriter, input ValidateMemberSettingsRequest) {
	if handler.game.ValidateMemberSettings == nil {
		refuse(response, errors.New("this game takes no member settings"))
		return
	}
	if err := handler.game.ValidateMemberSettings(ctx, input.LobbyID, input.Settings); err != nil {
		refuse(response, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (handler *hooksHandler) readyCheck(ctx context.Context, response http.ResponseWriter, input ReadyCheckRequest) {
	if handler.game.ReadyGate == nil {
		response.WriteHeader(http.StatusNoContent)
		return
	}
	check, err := handler.game.ReadyGate(ctx, input.LobbyID)
	if err != nil || check == nil {
		unavailableProblem(response)
		return
	}
	members := make([]domain.ReadyMember, 0, len(input.Members))
	for _, member := range input.Members {
		members = append(members, domain.ReadyMember{PlayerID: member.PlayerID, DisplayName: member.DisplayName, Settings: nilIfNull(member.Settings)})
	}
	err = check(members, input.AtStart)
	var requirement *domain.Error
	switch {
	case err == nil:
		response.WriteHeader(http.StatusNoContent)
	case errors.As(err, &requirement) && requirement.Code == domain.CodeGameRequirement:
		WriteProblem(response, Problem{Status: http.StatusConflict, Detail: requirement.Message, Code: string(domain.CodeGameRequirement), Type: requirement.Type})
	default:
		unavailableProblem(response)
	}
}

func (handler *hooksHandler) start(ctx context.Context, response http.ResponseWriter, input StartRequest) {
	if handler.game.StartGame == nil {
		unavailableProblem(response)
		return
	}
	gameID, err := handler.game.StartGame(ctx, input.Snapshot)
	if err != nil || gameID == "" {
		unavailableProblem(response)
		return
	}
	writeJSON(response, http.StatusOK, StartResponse{GameID: gameID})
}

func (handler *hooksHandler) roundEnded(ctx context.Context, response http.ResponseWriter, input RoundEndedRequest) {
	if handler.game.GameOver == nil {
		writeJSON(response, http.StatusOK, RoundEndedResponse{Ended: false})
		return
	}
	ended, err := handler.game.GameOver(ctx, input.LobbyID, input.Round)
	if err != nil {
		unavailableProblem(response)
		return
	}
	writeJSON(response, http.StatusOK, RoundEndedResponse{Ended: ended})
}

// WriteProblem answers with a Problem, as application/problem+json. A
// missing title is the status text.
func WriteProblem(response http.ResponseWriter, problem Problem) {
	if problem.Title == "" {
		problem.Title = http.StatusText(problem.Status)
	}
	response.Header().Set("Content-Type", ProblemContentType)
	response.WriteHeader(problem.Status)
	_ = json.NewEncoder(response).Encode(problem)
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
