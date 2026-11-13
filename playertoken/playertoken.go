// Package playertoken signs and verifies the lobby's player tokens: short-lived
// JWTs that tell a game which player sends a request, so the game needs
// neither the lobby's database nor a call to the lobby for each request.
//
// A token is a compact JWS signed with Ed25519 (alg EdDSA, RFC 8037), whose
// header names the signing key with kid. Its claims are iss, the lobby's
// public URL; sub, the player ID; aud, the game key; iat and exp, in Unix
// seconds; and name, the player's display name.
//
// The lobby signs with a KeySet and publishes its public keys as a JWKS. A
// game verifies tokens with a Verifier, which reads the public keys from a
// KeySource: a JWKSSource that fetches and caches the lobby's JWKS over HTTP,
// or the lobby's KeySet itself while the lobby and the game run in one
// process. The package is game-neutral and has no dependency on the rest of
// the lobby.
package playertoken

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Algorithm is the only JWS algorithm a token may name.
const Algorithm = "EdDSA"

// MaxLifetime is how long a token lasts at most. The lobby may issue shorter
// ones, and never one that outlives the player's session.
const MaxLifetime = 15 * time.Minute

// Leeway is how long after exp a Verifier still accepts a token, for clocks
// that differ a little.
const Leeway = 30 * time.Second

// maxTokenBytes bounds what Verify decodes. A real token is a few hundred bytes.
const maxTokenBytes = 4096

// ErrInvalid is returned for a token that is malformed, not signed by a
// current key of the lobby, of another issuer or audience, or expired. It says
// no more, on purpose.
var ErrInvalid = errors.New("the player token is not valid")

// Player is who a valid token stands for.
type Player struct {
	// ID is the lobby's player ID, the token's sub.
	ID string
	// Name is the player's display name when the token was issued.
	Name string
}

// Claims are a token's claims, as they are signed.
type Claims struct {
	Issuer   string `json:"iss"`
	Subject  string `json:"sub"`
	Audience string `json:"aud"`
	IssuedAt int64  `json:"iat"`
	Expires  int64  `json:"exp"`
	Name     string `json:"name"`
}

// header is a token's JOSE header.
type header struct {
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
	Type      string `json:"typ,omitempty"`
}

// KeySource finds the public key a token names with its kid.
type KeySource interface {
	// PublicKey returns the public key with keyID. A key the source doesn't
	// know is ErrInvalid (wrapped); any other error means the keys couldn't be
	// read, such as an unreachable lobby.
	PublicKey(ctx context.Context, keyID string) (ed25519.PublicKey, error)
}

// Verifier checks the tokens of one lobby.
type Verifier struct {
	issuer string
	keys   KeySource
	now    func() time.Time
}

// VerifierOption configures a Verifier.
type VerifierOption func(*Verifier)

// WithVerifierClock replaces time.Now; tests use it.
func WithVerifierClock(now func() time.Time) VerifierOption {
	return func(verifier *Verifier) { verifier.now = now }
}

// NewVerifier returns a Verifier for the tokens that issuer, the lobby's
// public URL, signs with the keys of source.
func NewVerifier(issuer string, source KeySource, options ...VerifierOption) (*Verifier, error) {
	if strings.TrimSpace(issuer) == "" {
		return nil, errors.New("playertoken: empty issuer")
	}
	if source == nil {
		return nil, errors.New("playertoken: nil key source")
	}
	verifier := &Verifier{issuer: issuer, keys: source, now: time.Now}
	for _, option := range options {
		option(verifier)
	}
	return verifier, nil
}

// Verify checks token for audience, the game's key, and returns its player.
// It checks the header's alg (only EdDSA) and kid, the Ed25519 signature with
// that key, iss, aud, and exp with Leeway. A token that fails any check is
// ErrInvalid; an error reading the keys is returned as it is.
func (verifier *Verifier) Verify(ctx context.Context, token, audience string) (Player, error) {
	if len(token) > maxTokenBytes {
		return Player{}, ErrInvalid
	}
	encodedHeader, rest, found := strings.Cut(token, ".")
	if !found {
		return Player{}, ErrInvalid
	}
	encodedClaims, encodedSignature, found := strings.Cut(rest, ".")
	if !found || strings.Contains(encodedSignature, ".") {
		return Player{}, ErrInvalid
	}
	var head header
	if err := decodeSegment(encodedHeader, &head); err != nil || head.Algorithm != Algorithm || head.KeyID == "" {
		return Player{}, ErrInvalid
	}
	signature, err := base64.RawURLEncoding.DecodeString(encodedSignature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return Player{}, ErrInvalid
	}
	key, err := verifier.keys.PublicKey(ctx, head.KeyID)
	if errors.Is(err, ErrInvalid) {
		return Player{}, ErrInvalid
	}
	if err != nil {
		return Player{}, fmt.Errorf("playertoken: read the lobby's keys: %w", err)
	}
	if !ed25519.Verify(key, []byte(encodedHeader+"."+encodedClaims), signature) {
		return Player{}, ErrInvalid
	}
	var claims Claims
	if err := decodeSegment(encodedClaims, &claims); err != nil {
		return Player{}, ErrInvalid
	}
	if claims.Issuer != verifier.issuer || claims.Audience != audience || audience == "" || claims.Subject == "" || claims.Expires == 0 {
		return Player{}, ErrInvalid
	}
	if !verifier.now().Before(time.Unix(claims.Expires, 0).Add(Leeway)) {
		return Player{}, ErrInvalid
	}
	return Player{ID: claims.Subject, Name: claims.Name}, nil
}

// FromAuthorization returns the credential of an Authorization header with
// the Bearer scheme, and false for any other header.
func FromAuthorization(authorization string) (string, bool) {
	scheme, credential, found := strings.Cut(strings.TrimSpace(authorization), " ")
	credential = strings.TrimSpace(credential)
	if !found || !strings.EqualFold(scheme, "Bearer") || credential == "" {
		return "", false
	}
	return credential, true
}

// decodeSegment decodes one base64url JSON segment of a token into value.
func decodeSegment(segment string, value any) error {
	data, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}
