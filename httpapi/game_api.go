package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"islandBattles/lobby/domain"
	"islandBattles/lobby/gamehooks"
)

// GameSecrets returns the shared secret of a game that the lobby calls over
// HTTP, by its key, or false for a game without one.
type GameSecrets func(gameKey string) ([]byte, bool)

var gameSignature = []map[string][]string{{"LobbySignature": {}}}

// GameProblem is a game API refusal.
type GameProblem struct {
	Title  string           `json:"title"`
	Status int              `json:"status"`
	Detail string           `json:"detail"`
	Code   domain.ErrorCode `json:"code" enum:"unauthenticated,not_found,permission_denied,watcher_kicked,invalid_input,unavailable,internal_error"`
	cause  error
}

func (problem *GameProblem) Error() string             { return problem.Detail }
func (problem *GameProblem) Unwrap() error             { return problem.cause }
func (problem *GameProblem) GetStatus() int            { return problem.Status }
func (problem *GameProblem) ContentType(string) string { return "application/problem+json" }

func gameProblem(status int, code domain.ErrorCode, detail string, cause error) *GameProblem {
	return &GameProblem{Title: http.StatusText(status), Status: status, Detail: detail, Code: code, cause: cause}
}

// gameAPIProblem answers an error of the lobby's services.
func gameAPIProblem(err error) error {
	switch domain.ErrorCodeOf(err) {
	case domain.CodeNotFound:
		return gameProblem(http.StatusNotFound, domain.CodeNotFound, "lobby not found", err)
	case domain.CodeUnavailable:
		return gameProblem(http.StatusServiceUnavailable, domain.CodeUnavailable, "service temporarily unavailable", err)
	default:
		return gameProblem(http.StatusInternalServerError, "internal_error", "internal server error", err)
	}
}

type gameLobbyInput struct {
	GameKey string `path:"game_key" pattern:"^[a-z0-9]+(?:-[a-z0-9]+)*$" maxLength:"64" doc:"The game's key: the one whose secret signed the request."`
	LobbyID string `path:"lobby_id" format:"uuid"`
}

type gameSnapshotOutput struct {
	Body domain.Snapshot
}

type authorizeWatchInput struct {
	GameKey string `path:"game_key" pattern:"^[a-z0-9]+(?:-[a-z0-9]+)*$" maxLength:"64" doc:"The game's key: the one whose secret signed the request."`
	LobbyID string `path:"lobby_id" format:"uuid"`
	Body    gamehooks.AuthorizeWatchRequest
}

// RegisterGameAPI installs the game API, which a game that the lobby calls
// over HTTP uses to read the lobby, under /lobby/v1/game-api/games/{game_key}.
// Every request is signed with the game's shared secret, as the hooks are
// (gamehooks.Sign); one that doesn't verify with the secret of the game key in
// its path is refused with 401, so a game reaches only its own lobbies. The
// game API is not part of the player API: hosts register it on an API of its
// own, whose OpenAPI document is lobby/api/game-api.openapi.json.
func RegisterGameAPI(api huma.API, games domain.GameAPI, secrets GameSecrets) {
	specification := api.OpenAPI()
	if specification.Components.SecuritySchemes == nil {
		specification.Components.SecuritySchemes = map[string]*huma.SecurityScheme{}
	}
	specification.Components.SecuritySchemes["LobbySignature"] = &huma.SecurityScheme{Type: "apiKey", In: "header", Name: gamehooks.SignatureHeader, Description: "t=<unix seconds>,v1=<hex HMAC-SHA256 of t + \".\" + method + \" \" + path + \".\" + body>, keyed with the game's shared secret. A time more than 5 minutes from the lobby's clock, either way, is refused."}
	group := huma.NewGroup(api, gamehooks.GameAPIPath)
	group.UseMiddleware(validationProblems)
	group.UseMiddleware(verifyGameSignature(secrets, time.Now))
	huma.Register(group, gameOperation(http.MethodGet, "/games/{game_key}/lobbies/{lobby_id}", "getGameLobby", http.StatusOK, "Read a lobby of the game",
		"Returns a lobby of the game as the game sees it: the snapshot of the player API, with every member and no player behind it, so membership_generation is 0. A lobby that is gone, or isn't the game's, is not_found. It reads without waiting for requests that change the lobby, so a game can call it while one of its hooks runs."),
		func(ctx context.Context, input *gameLobbyInput) (*gameSnapshotOutput, error) {
			snapshot, err := games.GameSnapshot(ctx, domain.Scope{GameKey: input.GameKey, LobbyID: input.LobbyID})
			if err != nil {
				return nil, gameAPIProblem(err)
			}
			return &gameSnapshotOutput{Body: snapshot}, nil
		})
	huma.Register(group, gameOperation(http.MethodPost, "/games/{game_key}/lobbies/{lobby_id}/authorize-watch", "authorizeGameWatch", http.StatusNoContent, "Check that a player may watch a lobby",
		"Answers 204 when player_id may watch the lobby, for the game's watcher reads: an active member, or a watcher the host hasn't kicked. Anyone else is refused with 403: watcher_kicked for a watcher the host kicked, and permission_denied for everyone else and for a lobby that is gone."),
		func(ctx context.Context, input *authorizeWatchInput) (*struct{}, error) {
			err := games.AuthorizeGameWatch(ctx, domain.Scope{GameKey: input.GameKey, LobbyID: input.LobbyID}, input.Body.PlayerID)
			switch domain.ErrorCodeOf(err) {
			case "":
				if err != nil {
					return nil, gameAPIProblem(err)
				}
				return nil, nil
			case domain.CodeWatcherKicked:
				return nil, gameProblem(http.StatusForbidden, domain.CodeWatcherKicked, domain.ErrWatcherKicked.Message, err)
			case domain.CodeNotFound:
				return nil, gameProblem(http.StatusForbidden, domain.CodePermissionDenied, "the player may not watch this lobby", err)
			default:
				return nil, gameAPIProblem(err)
			}
		})
	registry := specification.Components.Schemas
	problemSchema := registry.Schema(reflect.TypeOf(GameProblem{}), true, "GameProblem")
	for _, item := range specification.Paths {
		for _, operation := range []*huma.Operation{item.Get, item.Post} {
			if operation == nil {
				continue
			}
			for _, status := range []string{"401", "403", "404", "422", "500", "503"} {
				if response := operation.Responses[status]; response != nil {
					response.Content = map[string]*huma.MediaType{"application/problem+json": {Schema: problemSchema}}
				}
			}
		}
	}
}

func gameOperation(method, path, identifier string, status int, summary, description string) huma.Operation {
	errors := []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusUnprocessableEntity, http.StatusInternalServerError, http.StatusServiceUnavailable}
	if method == http.MethodPost {
		errors = []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusUnprocessableEntity, http.StatusInternalServerError, http.StatusServiceUnavailable}
	}
	return huma.Operation{Method: method, Path: path, OperationID: identifier, DefaultStatus: status, Tags: []string{"Game API"}, Summary: summary, Description: description, Security: gameSignature, Errors: errors}
}

// verifyGameSignature refuses, with 401, a request whose Lobby-Signature
// doesn't verify with the secret of the game key in its path, before the
// operation reads its input. The operation then reads the body that was
// verified.
func verifyGameSignature(secrets GameSecrets, now func() time.Time) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		refuse := func() {
			writeGameProblem(ctx, gameProblem(http.StatusUnauthorized, domain.CodeUnauthenticated, gamehooks.ErrBadSignature.Error(), nil))
		}
		var secret []byte
		found := false
		if secrets != nil {
			secret, found = secrets(ctx.Param("game_key"))
		}
		if !found {
			refuse()
			return
		}
		body, err := io.ReadAll(io.LimitReader(ctx.BodyReader(), gamehooks.MaxBodyBytes+1))
		if err != nil || len(body) > gamehooks.MaxBodyBytes {
			refuse()
			return
		}
		target := ctx.URL()
		if err := gamehooks.Verify(secret, ctx.Header(gamehooks.SignatureHeader), now(), ctx.Method(), target.EscapedPath(), body); err != nil {
			refuse()
			return
		}
		next(&verifiedBody{delegatedContext: ctx, body: bytes.NewReader(body)})
	}
}

// verifiedBody hands the operation the body that the signature covered.
type verifiedBody struct {
	delegatedContext
	body *bytes.Reader
}

func (ctx *verifiedBody) BodyReader() io.Reader { return ctx.body }

func writeGameProblem(ctx huma.Context, problem *GameProblem) {
	ctx.SetStatus(problem.Status)
	ctx.SetHeader("Content-Type", "application/problem+json")
	_ = json.NewEncoder(ctx.BodyWriter()).Encode(problem)
}
