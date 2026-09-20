// Package httpapi exposes the reusable, player-facing lobby API. It has no
// database, network, or environment dependency and deliberately excludes
// Centrifugo's trusted internal proxy endpoints.
package httpapi

import (
	"net/http"
	"reflect"

	"github.com/danielgtaylor/huma/v2"
	"islandBattles/lobby"
)

const basePath = "/lobby/v1"

var lobbyBearer = []map[string][]string{{"LobbyBearer": {}}}

// Register installs the public lobby API. app, authenticator and issuer are
// invoked only while serving requests, so offline OpenAPI export may use fakes.
func Register(api huma.API, app lobby.Application, authenticator lobby.Authenticator, issuer lobby.ConnectionTokenIssuer) {
	registerSchemas(api)
	handler := handlers{app: app, authenticator: authenticator, issuer: issuer}
	group := huma.NewGroup(api, basePath)
	group.UseMiddleware(validationProblems)
	protected := huma.NewGroup(group)
	protected.UseMiddleware(authenticate(authenticator))
	huma.Register(group, operation(http.MethodPost, "/sessions", "createLobbySession", http.StatusCreated, nil, "Create a guest session", "Creates one guest identity and bearer credential."), handler.session)
	huma.Register(protected, operation(http.MethodPost, "/connection-token", "createLobbyConnectionToken", http.StatusOK, lobbyBearer, "Create a connection token", "Creates an authenticated Centrifugo connection token; it never creates or joins a lobby."), handler.token)
	huma.Register(protected, operation(http.MethodPost, "/lobbies", "createLobby", http.StatusCreated, lobbyBearer, "Create a lobby", "Creates a lobby. Repeating the same request_id and payload returns the committed snapshot."), handler.create)
	huma.Register(protected, operation(http.MethodPost, "/memberships", "joinLobby", http.StatusOK, lobbyBearer, "Join a lobby", "Joins by game key and join code; started lobbies allow only frozen-roster rejoin."), handler.join)
	huma.Register(protected, operation(http.MethodGet, "/games/{game_key}/lobbies/{lobby_id}", "getLobby", http.StatusOK, lobbyBearer, "Get a lobby", "Returns the caller-authorized lobby snapshot."), handler.snapshot)
	huma.Register(protected, operation(http.MethodPut, "/games/{game_key}/lobbies/{lobby_id}/team", "selectLobbyTeam", http.StatusOK, lobbyBearer, "Select a team", "team_id is required and may be null to clear the selection."), handler.team)
	huma.Register(protected, operation(http.MethodPut, "/games/{game_key}/lobbies/{lobby_id}/ready", "setLobbyReady", http.StatusOK, lobbyBearer, "Set readiness", "ready is required; false explicitly clears readiness."), handler.ready)
	huma.Register(protected, operation(http.MethodPost, "/games/{game_key}/lobbies/{lobby_id}/start", "startLobby", http.StatusOK, lobbyBearer, "Start a lobby", "Starts an eligible lobby and freezes its roster."), handler.start)
	huma.Register(protected, operation(http.MethodDelete, "/games/{game_key}/lobbies/{lobby_id}/membership", "leaveLobby", http.StatusNoContent, lobbyBearer, "Leave a lobby", "Leaves durable membership across client instances."), handler.leave)
	huma.Register(protected, operation(http.MethodPost, "/games/{game_key}/lobbies/{lobby_id}/heartbeat", "heartbeatLobby", http.StatusOK, lobbyBearer, "Heartbeat a lease", "Renews one client lease; reconnecting does not create membership."), handler.heartbeat)
	huma.Register(protected, operation(http.MethodDelete, "/games/{game_key}/lobbies/{lobby_id}/leases/{client_instance_id}", "releaseLobbyLease", http.StatusNoContent, lobbyBearer, "Release a lease", "Releases one client lease without leaving durable membership."), handler.release)
	registerProblemResponses(api)
}
func operation(method, path, identifier string, status int, security []map[string][]string, summary, description string) huma.Operation {
	return huma.Operation{Method: method, Path: path, OperationID: identifier, DefaultStatus: status, Tags: []string{"Lobby"}, Summary: summary, Description: description, Security: security, Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusInternalServerError, http.StatusServiceUnavailable}}
}

func registerProblemResponses(api huma.API) {
	registry := api.OpenAPI().Components.Schemas
	problemSchema := registry.Schema(reflect.TypeOf(Problem{}), true, "Problem")
	for _, item := range api.OpenAPI().Paths {
		for _, operation := range []*huma.Operation{item.Get, item.Put, item.Post, item.Delete} {
			if operation == nil {
				continue
			}
			for _, status := range []string{"401", "403", "404", "409", "422", "500", "503"} {
				if response := operation.Responses[status]; response != nil {
					response.Content = map[string]*huma.MediaType{"application/problem+json": {Schema: problemSchema}}
				}
			}
		}
	}
}
func registerSchemas(api huma.API) {
	specification := api.OpenAPI()
	if specification.Components.SecuritySchemes == nil {
		specification.Components.SecuritySchemes = map[string]*huma.SecurityScheme{}
	}
	specification.Components.SecuritySchemes["LobbyBearer"] = &huma.SecurityScheme{Type: "http", Scheme: "bearer", Description: "Guest bearer credential issued by POST /lobby/v1/sessions. This is distinct from a Centrifugo connection JWT."}
	registerInvalidationSchema(specification.Components.Schemas)
}
