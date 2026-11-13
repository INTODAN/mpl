package playertoken

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// keyIDPattern is what a key ID may look like: short, and safe in a header,
// an environment variable and a URL.
var keyIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// JWKS is a JSON Web Key Set (RFC 7517) of the lobby's public keys.
type JWKS struct {
	Keys []JWK `json:"keys" doc:"The lobby's current public key, which signs new player tokens, and any previous ones that still verify tokens issued before a rotation."`
}

// JWK is one Ed25519 public key (RFC 8037).
type JWK struct {
	KeyType   string `json:"kty" enum:"OKP" doc:"Always OKP."`
	Curve     string `json:"crv" enum:"Ed25519" doc:"Always Ed25519."`
	X         string `json:"x" doc:"The 32-byte public key, base64url without padding."`
	KeyID     string `json:"kid" doc:"The key's ID, which a token's header names as kid."`
	Algorithm string `json:"alg" enum:"EdDSA" doc:"Always EdDSA."`
	Use       string `json:"use" enum:"sig" doc:"Always sig."`
}

// signingKey is one parsed key of a KeySet.
type signingKey struct {
	id      string
	private ed25519.PrivateKey
}

// KeySet holds the lobby's signing keys. The first signs; all are published
// and verify. It is also the in-process KeySource for a game that runs in
// the lobby's process.
type KeySet struct{ keys []signingKey }

// ParseKeySet reads a comma-separated list of kid:seed pairs, where seed is
// an Ed25519 seed of 32 bytes in base64url without padding, as the lobby's
// LOBBY_PLAYER_TOKEN_KEYS holds them. The first key signs.
func ParseKeySet(value string) (*KeySet, error) {
	set := &KeySet{}
	seen := map[string]bool{}
	for entry := range strings.SplitSeq(value, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		id, encodedSeed, found := strings.Cut(entry, ":")
		if !found || !keyIDPattern.MatchString(id) {
			return nil, errors.New("playertoken: each key must be kid:seed, with a kid of letters, digits, '.', '_' or '-'")
		}
		if seen[id] {
			return nil, fmt.Errorf("playertoken: key ID %q appears twice", id)
		}
		seed, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(encodedSeed))
		if err != nil || len(seed) != ed25519.SeedSize {
			return nil, fmt.Errorf("playertoken: the seed of key %q must be %d bytes in base64url without padding", id, ed25519.SeedSize)
		}
		seen[id] = true
		set.keys = append(set.keys, signingKey{id: id, private: ed25519.NewKeyFromSeed(seed)})
	}
	if len(set.keys) == 0 {
		return nil, errors.New("playertoken: no keys")
	}
	return set, nil
}

// Sign returns claims as a token signed with the set's first key.
func (set *KeySet) Sign(claims Claims) (string, error) {
	key := set.keys[0]
	encodedHeader, err := encodeSegment(header{Algorithm: Algorithm, KeyID: key.id, Type: "JWT"})
	if err != nil {
		return "", err
	}
	encodedClaims, err := encodeSegment(claims)
	if err != nil {
		return "", err
	}
	signingInput := encodedHeader + "." + encodedClaims
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(key.private, []byte(signingInput))), nil
}

// JWKS returns every key's public half, the signing key first.
func (set *KeySet) JWKS() JWKS {
	keys := make([]JWK, len(set.keys))
	for index, key := range set.keys {
		keys[index] = JWK{
			KeyType: "OKP", Curve: "Ed25519", Algorithm: Algorithm, Use: "sig", KeyID: key.id,
			X: base64.RawURLEncoding.EncodeToString(key.private.Public().(ed25519.PublicKey)),
		}
	}
	return JWKS{Keys: keys}
}

// PublicKey makes the set the in-process KeySource.
func (set *KeySet) PublicKey(_ context.Context, keyID string) (ed25519.PublicKey, error) {
	for _, key := range set.keys {
		if key.id == keyID {
			return key.private.Public().(ed25519.PublicKey), nil
		}
	}
	return nil, fmt.Errorf("unknown key %q: %w", keyID, ErrInvalid)
}

// publicKeys reads the Ed25519 signing keys of a JWKS. Keys of other types or
// uses are skipped, so a lobby may publish more later.
func publicKeys(set JWKS) map[string]ed25519.PublicKey {
	keys := map[string]ed25519.PublicKey{}
	for _, key := range set.Keys {
		if key.KeyType != "OKP" || key.Curve != "Ed25519" || key.KeyID == "" ||
			(key.Algorithm != "" && key.Algorithm != Algorithm) || (key.Use != "" && key.Use != "sig") {
			continue
		}
		public, err := base64.RawURLEncoding.DecodeString(key.X)
		if err != nil || len(public) != ed25519.PublicKeySize {
			continue
		}
		keys[key.KeyID] = ed25519.PublicKey(public)
	}
	return keys
}

func encodeSegment(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

var _ KeySource = (*KeySet)(nil)
