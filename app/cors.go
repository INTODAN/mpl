package app

import (
	"net/http"
	"slices"
	"strings"

	"islandBattles/lobby/gamehooks"
)

// The CORS answer to the games' sites, whose own lobby UI, such as a game's
// web client, calls the player API from another origin.
const (
	corsAllowedMethods = "GET, POST, PUT, DELETE"
	corsAllowedHeaders = "Authorization, Content-Type"
	// Browsers keep a preflight's answer for 10 minutes.
	corsMaxAge = "600"
	// Clients wait as long as a throttled answer's Retry-After says.
	corsExposedHeaders = "Retry-After"
	// playerAPIPath is where the player API is served.
	playerAPIPath = "/lobby/v1/"
)

// allowOrigins lets the games' sites, origins (LOBBY_ALLOWED_ORIGINS), call
// the player API under /lobby/v1/. Access-Control-Allow-Origin names the
// request's origin when it is one of them, never *, and a preflight from
// one of them is answered with 204, the methods and the headers that
// clients send (Authorization and Content-Type), cached for 10 minutes. Any
// other origin gets no CORS headers, and its preflight goes on to the
// router, as does every request outside the player API: the lobby page,
// the Centrifugo proxy and the game API, which games call from their
// servers. Without origins, nothing changes.
func allowOrigins(origins []string, next http.Handler) http.Handler {
	if len(origins) == 0 {
		return next
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		origin := request.Header.Get("Origin")
		path := request.URL.Path
		if origin == "" || !strings.HasPrefix(path, playerAPIPath) || strings.HasPrefix(path, gamehooks.GameAPIPath+"/") {
			next.ServeHTTP(response, request)
			return
		}
		headers := response.Header()
		headers.Add("Vary", "Origin")
		if !slices.Contains(origins, origin) {
			next.ServeHTTP(response, request)
			return
		}
		headers.Set("Access-Control-Allow-Origin", origin)
		if request.Method == http.MethodOptions && request.Header.Get("Access-Control-Request-Method") != "" {
			headers.Add("Vary", "Access-Control-Request-Method")
			headers.Add("Vary", "Access-Control-Request-Headers")
			headers.Set("Access-Control-Allow-Methods", corsAllowedMethods)
			headers.Set("Access-Control-Allow-Headers", corsAllowedHeaders)
			headers.Set("Access-Control-Max-Age", corsMaxAge)
			response.WriteHeader(http.StatusNoContent)
			return
		}
		headers.Set("Access-Control-Expose-Headers", corsExposedHeaders)
		next.ServeHTTP(response, request)
	})
}
