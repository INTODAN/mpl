package playertoken

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RefreshInterval is how often a JWKSSource fetches the keys at most.
const RefreshInterval = time.Minute

// DefaultMaxAge is how long a JWKSSource keeps keys whose response names no
// max-age. The lobby's JWKS says max-age=300.
const DefaultMaxAge = 5 * time.Minute

// maxJWKSBytes bounds the JWKS a source reads.
const maxJWKSBytes = 64 * 1024

// JWKSSource is the KeySource of a game that runs apart from the lobby: it
// fetches the lobby's JWKS, GET /lobby/v1/jwks.json, and keeps its keys for
// the response's max-age. A token that names a key it doesn't have makes it
// fetch the keys again, so a new key works at once, but it fetches at most
// once every RefreshInterval. When a fetch fails, it keeps using the keys it
// has.
type JWKSSource struct {
	url    string
	client *http.Client
	now    func() time.Time

	// mutex guards the fields below, and is held during a fetch, so
	// concurrent requests share it.
	mutex sync.Mutex
	keys  map[string]ed25519.PublicKey
	// fetched is when the keys were last fetched, or tried, and expires
	// when the keys stop being fresh.
	fetched time.Time
	expires time.Time
}

// JWKSOption configures a JWKSSource.
type JWKSOption func(*JWKSSource)

// WithHTTPClient fetches with client instead of one with a 10 s timeout.
func WithHTTPClient(client *http.Client) JWKSOption {
	return func(source *JWKSSource) { source.client = client }
}

// WithSourceClock replaces time.Now; tests use it.
func WithSourceClock(now func() time.Time) JWKSOption {
	return func(source *JWKSSource) { source.now = now }
}

// JWKSPath is where the lobby publishes its keys, under its URL.
const JWKSPath = "/lobby/v1/jwks.json"

// NewJWKSSource returns a source that reads the JWKS at url, such as
// https://lobby.example.org/lobby/v1/jwks.json. It fetches nothing until a
// key is needed.
func NewJWKSSource(url string, options ...JWKSOption) (*JWKSSource, error) {
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return nil, errors.New("playertoken: the JWKS URL must be http or https")
	}
	source := &JWKSSource{url: url, client: &http.Client{Timeout: 10 * time.Second}, now: time.Now}
	for _, option := range options {
		option(source)
	}
	return source, nil
}

// PublicKey returns the key with keyID, fetching the JWKS first when the keys
// are stale, and again when they don't have keyID.
func (source *JWKSSource) PublicKey(ctx context.Context, keyID string) (ed25519.PublicKey, error) {
	source.mutex.Lock()
	defer source.mutex.Unlock()
	now := source.now()
	var fetchErr error
	if source.keys == nil || !now.Before(source.expires) {
		fetchErr = source.refreshLocked(ctx, now)
	}
	if key, found := source.keys[keyID]; found {
		return key, nil
	}
	if fetchErr == nil {
		// A key the lobby added since the last fetch.
		fetchErr = source.refreshLocked(ctx, now)
		if key, found := source.keys[keyID]; found {
			return key, nil
		}
	}
	if fetchErr != nil {
		return nil, fetchErr
	}
	return nil, fmt.Errorf("unknown key %q: %w", keyID, ErrInvalid)
}

// refreshLocked fetches the keys unless it did less than RefreshInterval ago.
func (source *JWKSSource) refreshLocked(ctx context.Context, now time.Time) error {
	if !source.fetched.IsZero() && now.Sub(source.fetched) < RefreshInterval {
		return nil
	}
	source.fetched = now
	keys, maxAge, err := source.fetch(ctx)
	if err != nil {
		return err
	}
	source.keys = keys
	source.expires = now.Add(maxAge)
	return nil
}

func (source *JWKSSource) fetch(ctx context.Context) (map[string]ed25519.PublicKey, time.Duration, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source.url, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("playertoken: fetch the JWKS: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	response, err := source.client.Do(request)
	if err != nil {
		return nil, 0, fmt.Errorf("playertoken: fetch the JWKS: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("playertoken: fetch the JWKS: status %d", response.StatusCode)
	}
	var set JWKS
	if err := json.NewDecoder(io.LimitReader(response.Body, maxJWKSBytes)).Decode(&set); err != nil {
		return nil, 0, fmt.Errorf("playertoken: read the JWKS: %w", err)
	}
	return publicKeys(set), maxAge(response.Header.Get("Cache-Control")), nil
}

// maxAge reads a Cache-Control max-age, or DefaultMaxAge without one.
func maxAge(cacheControl string) time.Duration {
	for directive := range strings.SplitSeq(cacheControl, ",") {
		name, value, found := strings.Cut(strings.TrimSpace(directive), "=")
		if !found || !strings.EqualFold(name, "max-age") {
			continue
		}
		if seconds, err := strconv.Atoi(strings.Trim(value, `"`)); err == nil && seconds >= 0 {
			return time.Duration(seconds) * time.Second
		}
	}
	return DefaultMaxAge
}

var _ KeySource = (*JWKSSource)(nil)
