package app

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"islandBattles/lobby/domain"
	"islandBattles/lobby/gamehooks"
	"islandBattles/lobby/playertoken"
)

// The lobby's environment. Every name has the LOBBY_ prefix, so a host that
// runs the lobby beside its own code keeps the two apart.
const (
	DatabaseURLEnv                   = "LOBBY_DATABASE_URL"
	DatabaseMaxOpenConnectionsEnv    = "LOBBY_DATABASE_MAX_OPEN_CONNECTIONS"
	DatabaseMaxIdleConnectionsEnv    = "LOBBY_DATABASE_MAX_IDLE_CONNECTIONS"
	DatabaseConnectionMaxIdleTimeEnv = "LOBBY_DATABASE_CONNECTION_MAX_IDLE_TIME"
	// HTTPAddrEnv is read by the local server, cmd/lobby, alone.
	HTTPAddrEnv              = "LOBBY_HTTP_ADDR"
	PublicURLEnv             = "LOBBY_PUBLIC_URL"
	CentrifugoAPIURLEnv      = "LOBBY_CENTRIFUGO_API_URL"
	CentrifugoAPIKeyEnv      = "LOBBY_CENTRIFUGO_API_KEY"
	CentrifugoPublicURLEnv   = "LOBBY_CENTRIFUGO_PUBLIC_URL"
	ConnectionTokenSecretEnv = "LOBBY_CONNECTION_TOKEN_HMAC_SECRET"
	ProxySecretEnv           = "LOBBY_PROXY_SECRET"
	PlayerTokenKeysEnv       = "LOBBY_PLAYER_TOKEN_KEYS"
	PlayerTokenLifetimeEnv   = "LOBBY_PLAYER_TOKEN_LIFETIME"
	// AllowedOriginsEnv lists the games' sites whose own lobby UI calls the
	// player API from another origin, separated by commas.
	AllowedOriginsEnv = "LOBBY_ALLOWED_ORIGINS"
	// GamesEnv lists the games, gamehooks.GamesEnv.
	GamesEnv = gamehooks.GamesEnv
	// The retention settings, domain.RetentionPolicy; each one unset keeps
	// domain.DefaultRetention's value.
	RetentionIdlePeriodEnv      = "LOBBY_RETENTION_IDLE_PERIOD"
	RetentionMaximumAgeEnv      = "LOBBY_RETENTION_MAXIMUM_AGE"
	RetentionCreateRequestsEnv  = "LOBBY_RETENTION_CREATE_REQUESTS"
	RetentionExpiredSessionsEnv = "LOBBY_RETENTION_EXPIRED_SESSIONS"
	RetentionBatchSizeEnv       = "LOBBY_RETENTION_BATCH_SIZE"
	RetentionPassIntervalEnv    = "LOBBY_RETENTION_PASS_INTERVAL"
)

// DefaultHTTPAddr is where the local server listens without LOBBY_HTTP_ADDR.
const DefaultHTTPAddr = ":8890"

// The pool defaults suit Lambda, where an instance serves one request at a
// time and every instance has its own pool. The local server widens it; see
// cmd/lobby.
const (
	DefaultDatabaseMaxOpenConnections    = 2
	DefaultDatabaseMaxIdleConnections    = 2
	DefaultDatabaseConnectionMaxIdleTime = time.Minute
)

// DatabaseConfig says where PostgreSQL is and bounds the lobby's pool.
type DatabaseConfig struct {
	URL                   string
	MaxOpenConnections    int
	MaxIdleConnections    int
	ConnectionMaxIdleTime time.Duration
}

// Config is everything the lobby needs besides its database and Centrifugo
// connections: LoadConfig reads it from the LOBBY_* environment, and a host
// that runs the lobby in its own process fills it from its own settings.
type Config struct {
	// Database is read by OpenDatabase; New takes an open database.
	Database DatabaseConfig
	// PublicURL is where players and games reach the lobby, such as
	// https://lobby.example.org, and the iss of its player tokens.
	PublicURL string
	// CentrifugoAPIURL and CentrifugoAPIKey reach Centrifugo's HTTP API,
	// which the lobby pushes its changes to; see NewCentrifugoClient.
	CentrifugoAPIURL string
	CentrifugoAPIKey string
	// CentrifugoPublicURL is the WebSocket URL that connection tokens send
	// players to.
	CentrifugoPublicURL string
	// ConnectionTokenSecret signs Centrifugo connection tokens; Centrifugo
	// verifies them with the same secret.
	ConnectionTokenSecret string
	// ProxySecret is the header value Centrifugo sends with its subscribe
	// and refresh proxy calls.
	ProxySecret string
	// PlayerTokenKeys sign player tokens: playertoken.ParseKeySet.
	PlayerTokenKeys *playertoken.KeySet
	// PlayerTokenLifetime is how long a player token lasts, at most
	// playertoken.MaxLifetime; zero is playertoken.MaxLifetime.
	PlayerTokenLifetime time.Duration
	// Games is LOBBY_GAMES: a JSON array of gamehooks.GameConfig, at least
	// one game.
	Games string
	// GameSecrets are the values of the variables that the games name as
	// their secret_env, by name.
	GameSecrets map[string]string
	// Retention says when lobby data is deleted; a zero field keeps
	// domain.DefaultRetention's.
	Retention domain.RetentionPolicy
	// AllowedOrigins are the games' sites, such as
	// https://island-battles.example.org, whose own lobby UI calls the player
	// API from another origin: Handler answers them with CORS. A host that
	// runs the lobby in its own process serves its pages on the lobby's
	// origin and needs none.
	AllowedOrigins []string
}

// LookupGameSecret reads a game's shared secret by the variable that its
// secret_env names.
func (config Config) LookupGameSecret(name string) (string, bool) {
	secret, found := config.GameSecrets[name]
	return secret, found
}

// retention is Retention with domain.DefaultRetention's values where it has
// none.
func (config Config) retention() domain.RetentionPolicy {
	retention, defaults := config.Retention, domain.DefaultRetention()
	if retention.CreateRequests == 0 {
		retention.CreateRequests = defaults.CreateRequests
	}
	if retention.ExpiredSessions == 0 {
		retention.ExpiredSessions = defaults.ExpiredSessions
	}
	if retention.Lobbies.IdlePeriod == 0 {
		retention.Lobbies.IdlePeriod = defaults.Lobbies.IdlePeriod
	}
	if retention.Lobbies.MaximumAge == 0 {
		retention.Lobbies.MaximumAge = defaults.Lobbies.MaximumAge
	}
	if retention.BatchSize == 0 {
		retention.BatchSize = defaults.BatchSize
	}
	if retention.PassInterval == 0 {
		retention.PassInterval = defaults.PassInterval
	}
	return retention
}

// LoadConfig reads the lobby's settings from the environment.
func LoadConfig() (Config, error) {
	return LoadConfigFromLookup(os.LookupEnv)
}

// LoadConfigFromLookup reads and checks the lobby's settings through lookup,
// such as os.LookupEnv. Every setting is required except the pool limits,
// the player token lifetime and the retention settings, which have
// defaults, and the allowed origins, without which no other site may call
// the player API. Errors name variables, never their values.
func LoadConfigFromLookup(lookup func(string) (string, bool)) (Config, error) {
	if err := requireEnvironment(lookup, DatabaseURLEnv, PublicURLEnv, CentrifugoAPIURLEnv, CentrifugoAPIKeyEnv,
		CentrifugoPublicURLEnv, ConnectionTokenSecretEnv, ProxySecretEnv, PlayerTokenKeysEnv, GamesEnv); err != nil {
		return Config{}, err
	}
	database, err := LoadDatabaseConfigFromLookup(lookup)
	if err != nil {
		return Config{}, err
	}
	keys, err := playertoken.ParseKeySet(read(lookup, PlayerTokenKeysEnv))
	if err != nil {
		// The error names no seed.
		return Config{}, fmt.Errorf("%s: %w", PlayerTokenKeysEnv, err)
	}
	lifetime := playertoken.MaxLifetime
	if value := read(lookup, PlayerTokenLifetimeEnv); value != "" {
		lifetime, err = time.ParseDuration(value)
		if err != nil || lifetime < time.Minute || lifetime > playertoken.MaxLifetime {
			return Config{}, fmt.Errorf("%s must be a duration from 1m to 15m", PlayerTokenLifetimeEnv)
		}
	}
	retention, err := loadRetention(lookup)
	if err != nil {
		return Config{}, err
	}
	allowedOrigins, err := ParseOrigins(read(lookup, AllowedOriginsEnv))
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", AllowedOriginsEnv, err)
	}
	config := Config{
		Database:              database,
		PublicURL:             strings.TrimRight(read(lookup, PublicURLEnv), "/"),
		CentrifugoAPIURL:      read(lookup, CentrifugoAPIURLEnv),
		CentrifugoAPIKey:      read(lookup, CentrifugoAPIKeyEnv),
		CentrifugoPublicURL:   read(lookup, CentrifugoPublicURLEnv),
		ConnectionTokenSecret: read(lookup, ConnectionTokenSecretEnv),
		ProxySecret:           read(lookup, ProxySecretEnv),
		PlayerTokenKeys:       keys,
		PlayerTokenLifetime:   lifetime,
		Games:                 read(lookup, GamesEnv),
		Retention:             retention,
		AllowedOrigins:        allowedOrigins,
	}
	if err := validateURL(PublicURLEnv, config.PublicURL, "http", "https"); err != nil {
		return Config{}, err
	}
	if parsed, _ := url.Parse(config.PublicURL); parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return Config{}, fmt.Errorf("%s must be an origin, such as https://lobby.example.org, without a path", PublicURLEnv)
	}
	if err := validateURL(CentrifugoAPIURLEnv, config.CentrifugoAPIURL, "http", "https"); err != nil {
		return Config{}, err
	}
	if err := validateURL(CentrifugoPublicURLEnv, config.CentrifugoPublicURL, "ws", "wss"); err != nil {
		return Config{}, err
	}
	if config.GameSecrets, err = loadGameSecrets(lookup, config.Games); err != nil {
		return Config{}, err
	}
	games, err := gamehooks.ParseGames(config.Games, config.LookupGameSecret)
	if err != nil {
		return Config{}, err
	}
	if len(games) == 0 {
		return Config{}, fmt.Errorf("%s must list at least one game", GamesEnv)
	}
	policy := domain.DefaultPolicy(gamehooks.Registrations(games)...)
	policy.Retention = config.retention()
	if err := policy.Validate(); err != nil {
		return Config{}, fmt.Errorf("the lobby's retention settings or %s: %w", GamesEnv, err)
	}
	return config, nil
}

// loadGameSecrets reads the variables that the games of a LOBBY_GAMES value
// name as their secret_env.
func loadGameSecrets(lookup func(string) (string, bool), games string) (map[string]string, error) {
	var listed []struct {
		SecretEnv string `json:"secret_env"`
	}
	if err := json.Unmarshal([]byte(games), &listed); err != nil {
		return nil, fmt.Errorf("%s must be a JSON array: %w", GamesEnv, err)
	}
	secrets := map[string]string{}
	for _, game := range listed {
		if secret, found := lookup(game.SecretEnv); found && game.SecretEnv != "" {
			secrets[game.SecretEnv] = secret
		}
	}
	return secrets, nil
}

// ParseOrigins reads a list of origins separated by commas, such as
// "https://island-battles.example.org, http://localhost:3000": each an http
// or https origin as browsers send it in the Origin header, in lower case,
// without a path, a trailing slash or a default port. Empty, it lists none.
func ParseOrigins(value string) ([]string, error) {
	var origins []string
	for item := range strings.SplitSeq(value, ",") {
		origin := strings.TrimSpace(item)
		if origin == "" {
			continue
		}
		parsed, err := url.Parse(origin)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
			origin != strings.ToLower(parsed.Scheme+"://"+parsed.Host) ||
			(parsed.Scheme == "https" && parsed.Port() == "443") || (parsed.Scheme == "http" && parsed.Port() == "80") {
			return nil, fmt.Errorf("%q is not an origin such as https://game.example.org, without a path", origin)
		}
		origins = append(origins, origin)
	}
	return origins, nil
}

// LoadHTTPAddr reads the address that the local server listens on,
// DefaultHTTPAddr unless LOBBY_HTTP_ADDR names another.
func LoadHTTPAddr(lookup func(string) (string, bool)) (string, error) {
	address := read(lookup, HTTPAddrEnv)
	if address == "" {
		return DefaultHTTPAddr, nil
	}
	if _, _, err := net.SplitHostPort(address); err != nil {
		return "", fmt.Errorf("%s must be a host:port address: %w", HTTPAddrEnv, err)
	}
	return address, nil
}

// LoadDatabaseConfigFromLookup reads the database settings through lookup:
// LOBBY_DATABASE_URL, which is required, and the optional pool limits. They
// are all that the migrate command, cmd/migrate, reads.
func LoadDatabaseConfigFromLookup(lookup func(string) (string, bool)) (DatabaseConfig, error) {
	if err := requireEnvironment(lookup, DatabaseURLEnv); err != nil {
		return DatabaseConfig{}, err
	}
	config := DatabaseConfig{
		URL:                   read(lookup, DatabaseURLEnv),
		MaxOpenConnections:    DefaultDatabaseMaxOpenConnections,
		MaxIdleConnections:    DefaultDatabaseMaxIdleConnections,
		ConnectionMaxIdleTime: DefaultDatabaseConnectionMaxIdleTime,
	}
	if err := validateURL(DatabaseURLEnv, config.URL, "postgres", "postgresql"); err != nil {
		return DatabaseConfig{}, err
	}
	var err error
	if config.MaxOpenConnections, err = readCount(lookup, DatabaseMaxOpenConnectionsEnv, config.MaxOpenConnections, 1); err != nil {
		return DatabaseConfig{}, err
	}
	if config.MaxIdleConnections, err = readCount(lookup, DatabaseMaxIdleConnectionsEnv, min(config.MaxIdleConnections, config.MaxOpenConnections), 0); err != nil {
		return DatabaseConfig{}, err
	}
	if config.MaxIdleConnections > config.MaxOpenConnections {
		return DatabaseConfig{}, fmt.Errorf("%s must not exceed %s", DatabaseMaxIdleConnectionsEnv, DatabaseMaxOpenConnectionsEnv)
	}
	if config.ConnectionMaxIdleTime, err = readDuration(lookup, DatabaseConnectionMaxIdleTimeEnv, config.ConnectionMaxIdleTime); err != nil {
		return DatabaseConfig{}, err
	}
	return config, nil
}

// loadRetention reads the retention settings that are set; the policy
// checks them against each other.
func loadRetention(lookup func(string) (string, bool)) (domain.RetentionPolicy, error) {
	var retention domain.RetentionPolicy
	for _, setting := range []struct {
		name string
		into *time.Duration
	}{
		{RetentionIdlePeriodEnv, &retention.Lobbies.IdlePeriod},
		{RetentionMaximumAgeEnv, &retention.Lobbies.MaximumAge},
		{RetentionCreateRequestsEnv, &retention.CreateRequests},
		{RetentionExpiredSessionsEnv, &retention.ExpiredSessions},
		{RetentionPassIntervalEnv, &retention.PassInterval},
	} {
		duration, err := readDuration(lookup, setting.name, 0)
		if err != nil {
			return domain.RetentionPolicy{}, err
		}
		*setting.into = duration
	}
	batchSize, err := readCount(lookup, RetentionBatchSizeEnv, 0, 1)
	if err != nil {
		return domain.RetentionPolicy{}, err
	}
	if batchSize > domain.MaxRetentionBatchSize {
		return domain.RetentionPolicy{}, fmt.Errorf("%s must be at most %d", RetentionBatchSizeEnv, domain.MaxRetentionBatchSize)
	}
	retention.BatchSize = batchSize
	return retention, nil
}

func read(lookup func(string) (string, bool), name string) string {
	value, _ := lookup(name)
	return strings.TrimSpace(value)
}

// requireEnvironment names every missing variable at once, without values.
func requireEnvironment(lookup func(string) (string, bool), names ...string) error {
	var missing []string
	for _, name := range names {
		if read(lookup, name) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
}

// readCount reads an optional whole number of at least minimum.
func readCount(lookup func(string) (string, bool), name string, fallback, minimum int) (int, error) {
	value := read(lookup, name)
	if value == "" {
		return fallback, nil
	}
	count, err := strconv.Atoi(value)
	if err != nil || count < minimum {
		return 0, fmt.Errorf("%s must be a whole number of at least %d", name, minimum)
	}
	return count, nil
}

// readDuration reads an optional positive duration.
func readDuration(lookup func(string) (string, bool), name string, fallback time.Duration) (time.Duration, error) {
	value := read(lookup, name)
	if value == "" {
		return fallback, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration such as 1h", name)
	}
	return duration, nil
}

func validateURL(name, value string, schemes ...string) error {
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("%s must be an absolute URL", name)
	}
	for _, scheme := range schemes {
		if parsed.Scheme == scheme {
			return nil
		}
	}
	return fmt.Errorf("%s has unsupported URL scheme %q", name, parsed.Scheme)
}
