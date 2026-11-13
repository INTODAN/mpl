package gamehooks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"islandBattles/lobby/domain"
	"islandBattles/lobby/frontend"
)

// GamesEnv names the environment variable that lists the games the lobby
// calls over HTTP, as a JSON array of GameConfig.
const GamesEnv = "LOBBY_GAMES"

// GameConfig is one game in LOBBY_GAMES:
//
//	{"key": "example-game", "name": "Example Game",
//	 "hooks_url": "https://game.example.com/lobby-hooks",
//	 "secret_env": "LOBBY_GAME_SECRET_EXAMPLE_GAME",
//	 "start_rules": {"every_team_needs_member": true},
//	 "retention": {"idle_period": "2h", "maximum_age": "48h"},
//	 "frontend": {"result_url": "…", …}}
//
// The game's shared secret is the value of the environment variable that
// SecretEnv names, at least MinSecretBytes bytes. StartRules and Retention
// are optional and fixed by the configuration, as a registration's fields
// are. Frontend holds the lobby page's catalog entry, frontend.Game, whose
// key and name come from Key and Name.
type GameConfig struct {
	Key        string            `json:"key"`
	Name       string            `json:"name"`
	HooksURL   string            `json:"hooks_url"`
	SecretEnv  string            `json:"secret_env"`
	StartRules domain.StartRules `json:"start_rules"`
	Retention  RetentionConfig   `json:"retention"`
	Frontend   frontend.Game     `json:"frontend"`
}

// RetentionConfig overrides when a game's lobbies are deleted, as
// time.ParseDuration strings such as "2h". An empty field keeps the
// lobby's default.
type RetentionConfig struct {
	IdlePeriod string `json:"idle_period"`
	MaximumAge string `json:"maximum_age"`
}

// ConfiguredGame is a game from LOBBY_GAMES, ready for the host to add to
// its lobby policy, its frontend catalog and its game API.
type ConfiguredGame struct {
	Registration domain.GameRegistration
	Frontend     frontend.Game
	Secret       []byte
	Client       *Client
}

// LoadGames reads LOBBY_GAMES through lookupEnv, such as os.LookupEnv, with
// ParseGames. Unset or empty, it configures no game.
func LoadGames(lookupEnv func(string) (string, bool), options ...Option) ([]ConfiguredGame, error) {
	raw, _ := lookupEnv(GamesEnv)
	return ParseGames(raw, lookupEnv, options...)
}

// ParseGames reads a LOBBY_GAMES value: a JSON array of GameConfig, with
// unique keys and no unknown fields. Each game's secret is read with
// lookupEnv. An empty value configures no game.
func ParseGames(raw string, lookupEnv func(string) (string, bool), options ...Option) ([]ConfiguredGame, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	var configs []GameConfig
	if err := decoder.Decode(&configs); err != nil {
		return nil, fmt.Errorf("%s: %w", GamesEnv, err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: expected a single JSON array", GamesEnv)
	}
	games := make([]ConfiguredGame, 0, len(configs))
	seen := map[string]bool{}
	for index, config := range configs {
		game, err := config.configure(lookupEnv, options)
		if err != nil {
			return nil, fmt.Errorf("%s[%d]: %w", GamesEnv, index, err)
		}
		if seen[config.Key] {
			return nil, fmt.Errorf("%s[%d]: game key %q is listed twice", GamesEnv, index, config.Key)
		}
		seen[config.Key] = true
		games = append(games, game)
	}
	return games, nil
}

func (config GameConfig) configure(lookupEnv func(string) (string, bool), options []Option) (ConfiguredGame, error) {
	if err := domain.ValidateGameKey(config.Key); err != nil {
		return ConfiguredGame{}, fmt.Errorf("invalid game key %q", config.Key)
	}
	if strings.TrimSpace(config.Name) == "" {
		return ConfiguredGame{}, errors.New("name is required")
	}
	if config.SecretEnv == "" {
		return ConfiguredGame{}, errors.New("secret_env is required")
	}
	secret, _ := lookupEnv(config.SecretEnv)
	if len(secret) < MinSecretBytes {
		return ConfiguredGame{}, fmt.Errorf("%s must hold a secret of at least %d bytes", config.SecretEnv, MinSecretBytes)
	}
	retention, err := config.Retention.parse()
	if err != nil {
		return ConfiguredGame{}, err
	}
	client, err := NewClient(config.Key, config.HooksURL, []byte(secret), options...)
	if err != nil {
		return ConfiguredGame{}, fmt.Errorf("hooks_url: %w", err)
	}
	catalog := config.Frontend
	if (catalog.Key != "" && catalog.Key != config.Key) || (catalog.Name != "" && catalog.Name != config.Name) {
		return ConfiguredGame{}, errors.New("frontend takes its key and name from the game's")
	}
	catalog.Key, catalog.Name = config.Key, config.Name
	return ConfiguredGame{Registration: client.Registration(config.StartRules, retention), Frontend: catalog, Secret: bytes.Clone([]byte(secret)), Client: client}, nil
}

func (config RetentionConfig) parse() (domain.LobbyRetention, error) {
	var retention domain.LobbyRetention
	for _, field := range []struct {
		name, value string
		into        *time.Duration
	}{{"idle_period", config.IdlePeriod, &retention.IdlePeriod}, {"maximum_age", config.MaximumAge, &retention.MaximumAge}} {
		if field.value == "" {
			continue
		}
		duration, err := time.ParseDuration(field.value)
		if err != nil || duration <= 0 {
			return domain.LobbyRetention{}, fmt.Errorf("retention.%s must be a positive duration such as \"2h\"", field.name)
		}
		*field.into = duration
	}
	return retention, nil
}

// Registrations returns the games' registrations, for the lobby policy.
func Registrations(games []ConfiguredGame) []domain.GameRegistration {
	registrations := make([]domain.GameRegistration, 0, len(games))
	for _, game := range games {
		registrations = append(registrations, game.Registration)
	}
	return registrations
}

// Secrets returns a lookup of the games' shared secrets by game key, for the
// game API.
func Secrets(games []ConfiguredGame) func(gameKey string) ([]byte, bool) {
	secrets := make(map[string][]byte, len(games))
	for _, game := range games {
		secrets[game.Registration.Key] = bytes.Clone(game.Secret)
	}
	return func(gameKey string) ([]byte, bool) {
		secret, found := secrets[gameKey]
		return secret, found
	}
}
