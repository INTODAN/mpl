// Package app builds the whole lobby from a Config: its stores, guest
// sessions, player tokens and their public keys, the games of LOBBY_GAMES and
// the game API they read, the Centrifugo publisher, authorizer and proxy, the
// retention that lobby requests run, and the lobby page. Its two hosts serve
// it on their own, the local server in cmd/lobby and the Lambda function in
// cmd/lobby-lambda, and a game's host can run it in its own process with
// Register, so there is one wiring.
//
// New runs no schema changes: the local server migrates before it listens,
// and a deploy migrates before the Lambda function serves.
package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"islandBattles/lobby/application"
	"islandBattles/lobby/auth"
	"islandBattles/lobby/domain"
	"islandBattles/lobby/frontend"
	"islandBattles/lobby/gamehooks"
	"islandBattles/lobby/httpapi"
	"islandBattles/lobby/playertoken"
	lobbypostgres "islandBattles/lobby/postgres"
	"islandBattles/lobby/realtime"

	"github.com/centrifugal/gocent/v3"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
)

// UIPath is where the lobby page is served.
const UIPath = "/lobby/ui/"

// Observer is told what a host records about requests, such as the error
// lines of a game's host that runs the lobby in its own process. A nil field
// is told nothing.
type Observer struct {
	// Authenticated is told the player of each verified guest session.
	Authenticated func(ctx context.Context, playerID string)
	// Error is told the cause of a failure that the response leaves out: an
	// authentication that could not run, such as with the database down, and
	// a Centrifugo proxy call answered 503.
	Error func(ctx context.Context, err error)
}

// Option changes how New builds the lobby.
type Option func(*options)

type options struct {
	hooks    []gamehooks.Option
	observer Observer
}

// WithHookOptions passes options to the clients of the games' hooks, such as
// gamehooks.WithHTTPClient with a transport that reaches a game in the same
// process.
func WithHookOptions(hookOptions ...gamehooks.Option) Option {
	return func(selected *options) { selected.hooks = append(selected.hooks, hookOptions...) }
}

// WithObserver tells observer about requests.
func WithObserver(observer Observer) Option {
	return func(selected *options) { selected.observer = observer }
}

// Lobby is the lobby's services, ready to serve.
type Lobby struct {
	service        *application.Service
	authentication observedAuthentication
	playerTokens   *auth.PlayerTokens
	retention      *application.Retention
	proxy          *realtime.Proxy
	gameAPI        http.Handler
	catalog        []frontend.Game
	keys           *playertoken.KeySet
	allowedOrigins []string
}

// New builds the lobby on database, whose schema is up to date, and pushes
// its changes through centrifugo, the client of Centrifugo's HTTP API (see
// NewCentrifugoClient). It starts no goroutines and holds nothing that
// outlives a request besides database's pool, which the host owns and
// closes.
func New(config Config, database *gorm.DB, centrifugo *gocent.Client, logger *slog.Logger, opts ...Option) (*Lobby, error) {
	if database == nil || centrifugo == nil || logger == nil {
		return nil, errors.New("lobby app: missing dependency")
	}
	if config.PlayerTokenKeys == nil {
		return nil, errors.New("lobby app: no player token keys")
	}
	var selected options
	for _, option := range opts {
		option(&selected)
	}
	clock, identifiers := domain.SystemClock{}, domain.CryptoIdentifiers{}
	games, err := gamehooks.ParseGames(config.Games, config.LookupGameSecret, selected.hooks...)
	if err != nil {
		return nil, err
	}
	catalog := make([]frontend.Game, 0, len(games))
	for _, game := range games {
		catalog = append(catalog, game.Frontend)
	}
	policy := domain.DefaultPolicy(gamehooks.Registrations(games)...)
	policy.Retention = config.retention()
	// Requests push their own committed changes, so nothing runs between
	// requests.
	publisher, err := realtime.NewPublisher(centrifugo, logger, realtime.DefaultPushRetry())
	if err != nil {
		return nil, err
	}
	postgresStore, err := lobbypostgres.NewStore(database, clock)
	if err != nil {
		return nil, err
	}
	store, err := application.NewPublishingStore(postgresStore, publisher)
	if err != nil {
		return nil, err
	}
	identities, err := lobbypostgres.NewIdentityStore(database)
	if err != nil {
		return nil, err
	}
	authentication, err := auth.NewService(identities, clock, policy, identifiers, []byte(config.ConnectionTokenSecret), config.CentrifugoPublicURL)
	if err != nil {
		return nil, err
	}
	service, err := application.NewService(store, clock, identifiers, policy)
	if err != nil {
		return nil, err
	}
	retentionStore, err := lobbypostgres.NewRetentionStore(database)
	if err != nil {
		return nil, err
	}
	// Lobby requests delete expired lobby data in small batches as they end.
	retention, err := application.NewRetention(retentionStore, clock, policy, logger)
	if err != nil {
		return nil, err
	}
	authorizer, err := realtime.NewAuthorizer(identities, store, clock, policy)
	if err != nil {
		return nil, err
	}
	var proxyOptions []realtime.ProxyOption
	if report := selected.observer.Error; report != nil {
		// The proxy answers an outage with a bare 503.
		proxyOptions = append(proxyOptions, realtime.WithErrorReporter(report))
	}
	proxy, err := realtime.NewProxy(authorizer, config.ProxySecret, proxyOptions...)
	if err != nil {
		return nil, err
	}
	lifetime := config.PlayerTokenLifetime
	if lifetime == 0 {
		lifetime = playertoken.MaxLifetime
	}
	playerTokens, err := auth.NewPlayerTokens(config.PlayerTokenKeys, config.PublicURL, lifetime, clock, policy)
	if err != nil {
		return nil, err
	}
	gameAPIRouter := chi.NewMux()
	httpapi.RegisterGameAPI(humachi.New(gameAPIRouter, undocumented("Lobby game API")), service, gamehooks.Secrets(games))
	return &Lobby{
		service: service, authentication: observedAuthentication{authentication, selected.observer}, playerTokens: playerTokens,
		retention: retention, proxy: proxy, gameAPI: gameAPIRouter, catalog: catalog, keys: config.PlayerTokenKeys,
		allowedOrigins: config.AllowedOrigins,
	}, nil
}

// Register serves the lobby on router: the player API, under /lobby/v1, on
// api, which router serves; the game API, under gamehooks.GameAPIPath; the
// Centrifugo proxy, under /internal/lobby/centrifugo; and the lobby page,
// under UIPath. A host that runs the lobby in its own process registers it
// on its own router and API; the lobby's own hosts serve Handler.
func (lobby *Lobby) Register(router chi.Router, api huma.API) error {
	httpapi.Register(api, lobby.service, lobby.authentication, lobby.authentication, lobby.playerTokens, lobby.retention)
	router.Handle(gamehooks.GameAPIPath+"/*", lobby.gameAPI)
	lobby.proxy.Register(router)
	page, err := frontend.Handler(lobby.catalog)
	if err != nil {
		return err
	}
	router.Get(UIPath[:len(UIPath)-1], toPage)
	router.Handle(UIPath+"*", http.StripPrefix(UIPath, page))
	return nil
}

// Handler is the lobby on its own: Register's routes, with the player API's
// OpenAPI document at /openapi.json and its documentation at /docs, a
// redirect from / to the lobby page, and GET /healthz, which answers 204.
// The player API answers the configured AllowedOrigins with CORS.
func (lobby *Lobby) Handler() (http.Handler, error) {
	router := chi.NewMux()
	api := humachi.New(router, huma.DefaultConfig("Generic Lobby API", "1.0.0"))
	if err := lobby.Register(router, api); err != nil {
		return nil, err
	}
	router.Get("/", toPage)
	router.Get("/healthz", func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	})
	return allowOrigins(lobby.allowedOrigins, router), nil
}

// GameAPI serves the game API, under gamehooks.GameAPIPath, for a host that
// carries a game's calls to it in memory.
func (lobby *Lobby) GameAPI() http.Handler { return lobby.gameAPI }

// PlayerTokenKeys are the keys that sign player tokens. A game in the
// lobby's process verifies tokens with them, as a playertoken.KeySource,
// instead of the lobby's JWKS.
func (lobby *Lobby) PlayerTokenKeys() *playertoken.KeySet { return lobby.keys }

func toPage(response http.ResponseWriter, request *http.Request) {
	http.Redirect(response, request, UIPath, http.StatusTemporaryRedirect)
}

// undocumented is an API's configuration without documentation routes, for
// an API that shares a router with another.
func undocumented(title string) huma.Config {
	config := huma.DefaultConfig(title, "1.0.0")
	config.OpenAPIPath, config.DocsPath, config.SchemasPath = "", "", ""
	return config
}

// observedAuthentication tells the observer about each verified session and
// each authentication that could not run.
type observedAuthentication struct {
	*auth.Service
	observer Observer
}

func (authentication observedAuthentication) Authenticate(ctx context.Context, authorization string) (domain.SessionVerification, error) {
	verified, err := authentication.Service.Authenticate(ctx, authorization)
	switch {
	case err == nil:
		if authenticated := authentication.observer.Authenticated; authenticated != nil {
			authenticated(ctx, verified.Principal.PlayerID)
		}
	case !errors.Is(err, domain.ErrUnauthenticated):
		// The lobby's authentication answers with its own problem, which
		// leaves the cause out, such as an unreachable database.
		if report := authentication.observer.Error; report != nil {
			report(ctx, err)
		}
	}
	return verified, err
}
