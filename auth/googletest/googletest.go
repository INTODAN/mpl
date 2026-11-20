// Package googletest signs ID tokens the way Google does, with a key of its
// own, for tests and live suites of Sign in with Google. A lobby verifies
// them when its Google certificate URL (LOBBY_GOOGLE_CERTS_URL) points at an
// Issuer's Handler. Never use it in production.
package googletest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"strconv"
	"time"
)

// Issuer is a test identity provider with one RSA key.
type Issuer struct {
	key   *rsa.PrivateKey
	keyID string
}

// NewIssuer makes an issuer with a new 2048-bit key named keyID.
func NewIssuer(keyID string) (*Issuer, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	return &Issuer{key: key, keyID: keyID}, nil
}

// Claims are an ID token's claims. Extra claims are added as they are.
type Claims struct {
	Issuer    string
	Audience  string
	Subject   string
	ExpiresAt time.Time
	Extra     map[string]any
}

// Token signs claims with the issuer's key, as RS256.
func (issuer *Issuer) Token(claims Claims) (string, error) {
	return issuer.sign(map[string]any{"alg": "RS256", "kid": issuer.keyID, "typ": "JWT"}, claims, issuer.key)
}

// GoogleToken signs a valid Google ID token for subject and audience that
// expires in an hour from now.
func (issuer *Issuer) GoogleToken(subject, audience string, now time.Time) (string, error) {
	return issuer.Token(Claims{Issuer: "https://accounts.google.com", Audience: audience, Subject: subject, ExpiresAt: now.Add(time.Hour)})
}

// SignWith signs claims with another key under the issuer's key ID, as a
// forger would.
func (issuer *Issuer) SignWith(key *rsa.PrivateKey, claims Claims) (string, error) {
	return issuer.sign(map[string]any{"alg": "RS256", "kid": issuer.keyID, "typ": "JWT"}, claims, key)
}

func (issuer *Issuer) sign(header map[string]any, claims Claims, key *rsa.PrivateKey) (string, error) {
	payload := map[string]any{"iss": claims.Issuer, "aud": claims.Audience, "sub": claims.Subject, "exp": claims.ExpiresAt.Unix(), "iat": claims.ExpiresAt.Add(-time.Hour).Unix()}
	for name, value := range claims.Extra {
		payload[name] = value
	}
	encodedHeader, err := encode(header)
	if err != nil {
		return "", err
	}
	encodedPayload, err := encode(payload)
	if err != nil {
		return "", err
	}
	signed := encodedHeader + "." + encodedPayload
	digest := sha256.Sum256([]byte(signed))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return signed + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func encode(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

// Handler serves the issuer's public key as Google's certificate URL does,
// a JWKS cached for maxAge.
func (issuer *Issuer) Handler(maxAge time.Duration) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		public := issuer.key.PublicKey
		document := map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "alg": "RS256", "use": "sig", "kid": issuer.keyID,
			"n": base64.RawURLEncoding.EncodeToString(public.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(public.E)).Bytes()),
		}}}
		response.Header().Set("Content-Type", "application/json")
		response.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(int(maxAge/time.Second)))
		_ = json.NewEncoder(response).Encode(document)
	})
}
