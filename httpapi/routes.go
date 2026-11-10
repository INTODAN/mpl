// Package httpapi exposes the reusable, player-facing lobby API. It has no
// database, network, or environment dependency and deliberately excludes
// Centrifugo's trusted internal proxy endpoints.
package httpapi

import (
	"net/http"
	"reflect"

	"github.com/danielgtaylor/huma/v2"
	"islandBattles/lobby/domain"
)

const basePath = "/lobby/v1"

var lobbyBearer = []map[string][]string{{"LobbyBearer": {}}}

// Register installs the public lobby API. app, authenticator, issuer and
// retention are invoked only while serving requests, so offline OpenAPI export
// may use fakes. After each lobby request, retention runs a pass if one is
// due; a nil retention deletes nothing.
func Register(api huma.API, app domain.Application, authenticator domain.Authenticator, issuer domain.ConnectionTokenIssuer, retention domain.RetentionRunner) {
	registerSchemas(api)
	handler := handlers{app: app, authenticator: authenticator, issuer: issuer}
	group := huma.NewGroup(api, basePath)
	group.UseMiddleware(validationProblems)
	group.UseTransformer(BoundErrorDetails)
	if retention != nil {
		group.UseMiddleware(runRetentionAfter(retention))
	}
	protected := huma.NewGroup(group)
	protected.UseMiddleware(authenticate(authenticator))
	huma.Register(group, operation(http.MethodPost, "/sessions", "createLobbySession", http.StatusCreated, nil, "Create a guest session", "Creates one guest identity and bearer credential."), handler.session)
	huma.Register(protected, operation(http.MethodPost, "/connection-token", "createLobbyConnectionToken", http.StatusOK, lobbyBearer, "Create a connection token", "Creates an authenticated Centrifugo connection token; it never creates or joins a lobby."), handler.token)
	huma.Register(protected, operation(http.MethodPost, "/lobbies", "createLobby", http.StatusCreated, lobbyBearer, "Create a lobby", "Creates a lobby with its teams, from 2 to 16 with unique names. A team holds one member, a person or a seat, so the lobby holds as many members as it has teams; its capacity in the snapshot is that number. The optional game_settings object is checked by the game now; invalid settings fail with invalid_game_settings. The lobby stores the settings without reading them and returns them in every snapshot, including the started one. The host can change them, with the teams, while the lobby waits (PUT …/settings). Repeating the same request_id and payload within the idempotency window, 24 hours by default, returns the committed snapshot. The lobby is deleted, in any phase, once nobody has been connected to it for an hour, and in any case a day after creation, unless its game sets other times."), handler.create)
	huma.Register(protected, operation(http.MethodPost, "/memberships", "joinLobby", http.StatusOK, lobbyBearer, "Join a lobby", "Joins by game key and join code; a lobby with as many members as teams is full (lobby_full). Started lobbies allow only frozen-roster rejoin; once a member reopens the lobby after its game, it waits and anyone with the code can join again."), handler.join)
	huma.Register(protected, operation(http.MethodGet, "/games/{game_key}/lobbies/{lobby_id}", "getLobby", http.StatusOK, lobbyBearer, "Get a lobby", "Returns the caller-authorized lobby snapshot."), handler.snapshot)
	huma.Register(protected, operation(http.MethodPut, "/games/{game_key}/lobbies/{lobby_id}/team", "selectLobbyTeam", http.StatusOK, lobbyBearer, "Select a team", "team_id is required and may be null to clear the selection. A team holds one member: a team that already has a person or a seat is team_full."), handler.team)
	huma.Register(protected, operation(http.MethodPut, "/games/{game_key}/lobbies/{lobby_id}/ready", "setLobbyReady", http.StatusOK, lobbyBearer, "Set readiness", "ready is required; false explicitly clears readiness. A member needs a team to get ready, or it fails with not_ready. A game can also require something of its own first, such as a choice made in the game's API: then getting ready fails with 409 game_requirement, whose type is the game's problem type and whose detail says what to do."), handler.ready)
	huma.Register(protected, operation(http.MethodPost, "/games/{game_key}/lobbies/{lobby_id}/start", "startLobby", http.StatusOK, lobbyBearer, "Start a lobby", "Starts an eligible lobby and freezes its roster. The host can start once at least two members are present and every member has chosen a team, is ready and is connected; otherwise it fails with not_ready. The snapshot's start_rules list the game's extra conditions: with every_team_needs_member, a lobby with an empty team fails with empty_teams, whose team_ids name the empty teams. A game that requires something of each member before they get ready checks the whole roster again, and fails with game_requirement for a member who doesn't meet it. The roster stays frozen while the lobby is started; once the game has ended, a member can reopen the lobby for another game (reopenLobby), which empties the roster for the next start."), handler.start)
	huma.Register(protected, operation(http.MethodPost, "/games/{game_key}/lobbies/{lobby_id}/reopen", "reopenLobby", http.StatusOK, lobbyBearer, "Reopen a lobby after its game", "Brings a started lobby whose game has ended back to waiting, so its members can play another game in it, and returns the snapshot. Any active person in the lobby may reopen it; anyone else gets not_found. The lobby's round goes up by one, and the game binds the next game to the new round. Members, teams, seats, the game settings and the host stay; every person's ready is cleared, seats stay ready, and the start roster is emptied until the next start. The change publishes lobby.changed. A lobby that already waits is returned unchanged, so two members reopening it at once both succeed. A lobby whose game is still running fails with game_running; a closed lobby, or one of a game that doesn't let its lobbies play again, fails with phase_conflict. While the lobby waits, people can join it and the host can change its settings as before a first game."), handler.reopen)
	huma.Register(protected, operation(http.MethodPut, "/games/{game_key}/lobbies/{lobby_id}/settings", "changeLobbySettings", http.StatusOK, lobbyBearer, "Change a lobby's settings", "Replaces the lobby's game_settings and teams, and returns the snapshot. Only the host can change them (otherwise not_host), and only while the lobby waits (otherwise phase_conflict). The body has the rules of a create request: from 2 to 16 teams with unique names (otherwise invalid_input), and an optional game_settings object that the game checks against the new teams (otherwise invalid_game_settings). The teams are rebuilt by position: the lobby's first team keeps its ID and takes the first name, and so on. New teams are added at the end. Teams beyond the new count are removed: a person on one stays in the lobby without a team, and a seat on one is removed, as removeLobbySeat removes it. Every person's ready is cleared, seats stay ready, and the lobby's capacity becomes the new number of teams. A change that would leave more members than teams, people and the seats that keep their team, fails with lobby_full and changes nothing. Settings and teams equal to the lobby's change nothing: the snapshot comes back with the same version and nobody's ready is cleared. Otherwise the change publishes lobby.changed."), handler.changeSettings)
	huma.Register(protected, operation(http.MethodPost, "/games/{game_key}/lobbies/{lobby_id}/seats", "addLobbySeat", http.StatusOK, lobbyBearer, "Add a seat", "Adds a seat that no person plays, such as the game's bot, to a team, and returns the snapshot, where the seat is a member of kind seat. Only the host can add one (otherwise not_host), and only while the lobby waits (otherwise phase_conflict). The game checks seat_settings and names the seat; settings it doesn't understand fail with invalid_seat_settings, and a game without seats refuses every one. The seat takes its team, which then holds no other member, and counts toward the lobby's capacity: a team that already has a person or a seat is team_full, and a lobby with as many members as teams is lobby_full. It counts toward the members a start needs, is always ready, never disconnects, and joins the start roster with its own player_id. A start still needs the host, a person. A team_id that isn't one of the lobby's teams is invalid_input. The optional request_id makes a retry after a lost response safe: repeating it with the same team_id and seat_settings returns the current snapshot without a second seat, and with another team or settings fails with idempotency_conflict. Without one, each call adds a seat."), handler.addSeat)
	huma.Register(protected, operation(http.MethodDelete, "/games/{game_key}/lobbies/{lobby_id}/seats/{seat_id}", "removeLobbySeat", http.StatusOK, lobbyBearer, "Remove a seat", "Removes a seat that the host added, by its member ID, and returns the snapshot. Only the host can remove one, and only while the lobby waits. Removing a seat again changes nothing; an ID that names no seat of the lobby is invalid_input."), handler.removeSeat)
	huma.Register(protected, operation(http.MethodDelete, "/games/{game_key}/lobbies/{lobby_id}/membership", "leaveLobby", http.StatusNoContent, lobbyBearer, "Leave a lobby", "Leaves durable membership across client instances."), handler.leave)
	huma.Register(protected, operation(http.MethodPost, "/watches", "watchLobby", http.StatusOK, lobbyBearer, "Watch a lobby", "Lets anyone with a guest session and the lobby's join code watch it, in any phase: waiting, started or closed while it still exists. Adds your watcher row, or finds the one you have, and returns the lobby and its watch channel, where its game publishes what watchers may see; the game documents its own watcher reads. A watcher takes no seat and counts toward nothing: not the lobby's capacity, the start roster, leases, the grace period or the lobby's retention. Starting to watch changes the snapshot's watcher_count. An active member of the lobby gets no watcher row and keeps their member access (member is true). A wrong code, or a code of another game, is not_found; a watcher the host kicked is refused with watcher_kicked, and a new watcher beyond the lobby's limit of watchers with watchers_full."), handler.watch)
	huma.Register(protected, operation(http.MethodDelete, "/games/{game_key}/lobbies/{lobby_id}/watch", "unwatchLobby", http.StatusNoContent, lobbyBearer, "Stop watching a lobby", "Removes your watcher row and unsubscribes you from the watch channel. A kicked watcher stays kicked. Without a watcher row it is not_found."), handler.unwatch)
	huma.Register(protected, operation(http.MethodGet, "/games/{game_key}/lobbies/{lobby_id}/watchers", "listLobbyWatchers", http.StatusOK, lobbyBearer, "List a lobby's watchers", "Lists the watchers the host hasn't kicked, with their display names, for the lobby's active members."), handler.watchers)
	huma.Register(protected, operation(http.MethodDelete, "/games/{game_key}/lobbies/{lobby_id}/watchers/{player_id}", "kickLobbyWatcher", http.StatusNoContent, lobbyBearer, "Kick a watcher", "Lets the host remove a watcher, in any phase. The watcher is unsubscribed from the watch channel at once and can't watch again with that session and the join code (watcher_kicked); the kick bars the player ID, so a new guest session can watch again, and in a waiting lobby the kicked player can still join as a member. Kicking them again changes nothing; a player who isn't a watcher of the lobby is not_found."), handler.kickWatcher)
	huma.Register(protected, operation(http.MethodPost, "/games/{game_key}/lobbies/{lobby_id}/heartbeat", "heartbeatLobby", http.StatusOK, lobbyBearer, "Heartbeat a lease", "Renews one client lease; reconnecting does not create membership."), handler.heartbeat)
	huma.Register(protected, operation(http.MethodDelete, "/games/{game_key}/lobbies/{lobby_id}/leases/{client_instance_id}", "releaseLobbyLease", http.StatusNoContent, lobbyBearer, "Release a lease", "Releases one client lease without leaving durable membership."), handler.release)
	registerProblemResponses(api)
}

// runRetentionAfter runs the retention pass once the request is handled, so
// lobby data is deleted only while the lobby is in use.
func runRetentionAfter(retention domain.RetentionRunner) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		next(ctx)
		retention.RunIfDue(ctx.Context())
	}
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
