package auth

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/INTODAN/mpl/domain"
)

// GoogleCertsURL publishes the keys that sign Google's ID tokens.
const GoogleCertsURL = "https://www.googleapis.com/oauth2/v3/certs"

// googleIssuers are the iss values of Google's ID tokens.
var googleIssuers = []string{"accounts.google.com", "https://accounts.google.com"}

// IDTokenVerifier checks an identity provider's ID token and returns its
// subject, the provider's ID of the account. An invalid token is
// domain.ErrUnauthenticated; keys that can't be fetched are
// domain.ErrUnavailable.
type IDTokenVerifier interface {
	Verify(ctx context.Context, token string) (subject string, err error)
}

// GoogleVerifier verifies Google ID tokens: an RS256 signature by one of the
// keys at CertsURL, which it caches as their Cache-Control says, an iss of
// Google's, an aud that is one of ClientIDs, and an exp in the future. It
// trusts no claim but sub.
type GoogleVerifier struct {
	clientIDs []string
	certsURL  string
	client    *http.Client
	clock     domain.Clock

	mutex     sync.Mutex
	keys      map[string]*rsa.PublicKey
	expiresAt time.Time
	fetchedAt time.Time
}

// NewGoogleVerifier verifies the ID tokens of clientIDs with the keys at
// certsURL, GoogleCertsURL in production. A nil client is
// http.DefaultClient.
func NewGoogleVerifier(clientIDs []string, certsURL string, client *http.Client, clock domain.Clock) (*GoogleVerifier, error) {
	if len(clientIDs) == 0 || certsURL == "" || clock == nil {
		return nil, errors.New("lobby auth: a Google verifier needs client IDs, a certificate URL and a clock")
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &GoogleVerifier{clientIDs: slices.Clone(clientIDs), certsURL: certsURL, client: client, clock: clock}, nil
}

// keyRefetchInterval is how often an unknown kid may fetch the keys again
// before the cached ones expire.
const keyRefetchInterval = time.Minute

func (verifier *GoogleVerifier) Verify(ctx context.Context, token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", domain.ErrUnauthenticated
	}
	var header struct {
		Algorithm string `json:"alg"`
		KeyID     string `json:"kid"`
	}
	if decodeSegment(parts[0], &header) != nil || header.Algorithm != "RS256" || header.KeyID == "" {
		return "", domain.ErrUnauthenticated
	}
	key, err := verifier.key(ctx, header.KeyID)
	if err != nil {
		return "", err
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", domain.ErrUnauthenticated
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature) != nil {
		return "", domain.ErrUnauthenticated
	}
	var claims struct {
		Issuer     string          `json:"iss"`
		Audience   json.RawMessage `json:"aud"`
		Subject    string          `json:"sub"`
		Expiration json.Number     `json:"exp"`
	}
	if decodeSegment(parts[1], &claims) != nil || !slices.Contains(googleIssuers, claims.Issuer) || claims.Subject == "" {
		return "", domain.ErrUnauthenticated
	}
	var audience string
	if json.Unmarshal(claims.Audience, &audience) != nil || !slices.Contains(verifier.clientIDs, audience) {
		return "", domain.ErrUnauthenticated
	}
	expiration, err := claims.Expiration.Int64()
	if err != nil || !verifier.clock.Now().Before(time.Unix(expiration, 0)) {
		return "", domain.ErrUnauthenticated
	}
	return claims.Subject, nil
}

func decodeSegment(segment string, into any) error {
	decoded, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(strings.NewReader(string(decoded)))
	decoder.UseNumber()
	return decoder.Decode(into)
}

// key returns the cached key named kid, fetching the keys when they expired,
// or, at most once a minute, when kid is unknown.
func (verifier *GoogleVerifier) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	verifier.mutex.Lock()
	defer verifier.mutex.Unlock()
	now := verifier.clock.Now()
	key, known := verifier.keys[kid]
	if known && now.Before(verifier.expiresAt) {
		return key, nil
	}
	if verifier.keys != nil && now.Before(verifier.expiresAt) && now.Sub(verifier.fetchedAt) < keyRefetchInterval {
		return nil, domain.ErrUnauthenticated
	}
	keys, maxAge, err := verifier.fetch(ctx)
	if err != nil {
		return nil, &domain.Error{Code: domain.CodeUnavailable, Message: domain.ErrUnavailable.Message, Cause: err}
	}
	verifier.keys, verifier.fetchedAt, verifier.expiresAt = keys, now, now.Add(maxAge)
	if key, known = keys[kid]; !known {
		return nil, domain.ErrUnauthenticated
	}
	return key, nil
}

func (verifier *GoogleVerifier) fetch(ctx context.Context) (map[string]*rsa.PublicKey, time.Duration, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, verifier.certsURL, nil)
	if err != nil {
		return nil, 0, err
	}
	response, err := verifier.client.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("fetching Google's keys answered %d", response.StatusCode)
	}
	var document struct {
		Keys []struct {
			KeyType   string `json:"kty"`
			KeyID     string `json:"kid"`
			Algorithm string `json:"alg"`
			Modulus   string `json:"n"`
			Exponent  string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&document); err != nil {
		return nil, 0, fmt.Errorf("reading Google's keys: %w", err)
	}
	keys := map[string]*rsa.PublicKey{}
	for _, entry := range document.Keys {
		if entry.KeyType != "RSA" || entry.KeyID == "" || (entry.Algorithm != "" && entry.Algorithm != "RS256") {
			continue
		}
		modulus, modulusErr := base64.RawURLEncoding.DecodeString(entry.Modulus)
		exponent, exponentErr := base64.RawURLEncoding.DecodeString(entry.Exponent)
		if modulusErr != nil || exponentErr != nil || len(exponent) == 0 || len(exponent) > 4 {
			continue
		}
		keys[entry.KeyID] = &rsa.PublicKey{N: new(big.Int).SetBytes(modulus), E: int(new(big.Int).SetBytes(exponent).Int64())}
	}
	if len(keys) == 0 {
		return nil, 0, errors.New("google published no RS256 keys")
	}
	return keys, maxAge(response.Header.Get("Cache-Control")), nil
}

// defaultKeysMaxAge is how long keys whose response names no max-age are
// kept.
const defaultKeysMaxAge = 5 * time.Minute

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
	return defaultKeysMaxAge
}
