package playertoken_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"islandBattles/lobby/playertoken"
)

const issuer = "https://lobby.example.test"

// Seeds of 32 bytes, base64url without padding.
var (
	firstSeed  = base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("a", 32)))
	secondSeed = base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
)

func keySet(t *testing.T, value string) *playertoken.KeySet {
	t.Helper()
	set, err := playertoken.ParseKeySet(value)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func claims(now time.Time) playertoken.Claims {
	return playertoken.Claims{Issuer: issuer, Subject: "4f7c0a2e-58c4-4a51-9d2e-3c1f0f1d2a3b", Audience: "island-battles",
		IssuedAt: now.Unix(), Expires: now.Add(playertoken.MaxLifetime).Unix(), Name: "Ada"}
}

func sign(t *testing.T, set *playertoken.KeySet, value playertoken.Claims) string {
	t.Helper()
	token, err := set.Sign(value)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func verifier(t *testing.T, source playertoken.KeySource, now *time.Time) *playertoken.Verifier {
	t.Helper()
	verifier, err := playertoken.NewVerifier(issuer, source, playertoken.WithVerifierClock(func() time.Time { return *now }))
	if err != nil {
		t.Fatal(err)
	}
	return verifier
}

func segment(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(data)
}

func TestAnIssuedTokenVerifiesAsItsPlayer(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	set := keySet(t, "k1:"+firstSeed)
	player, err := verifier(t, set, &now).Verify(context.Background(), sign(t, set, claims(now)), "island-battles")
	if err != nil {
		t.Fatal(err)
	}
	if player != (playertoken.Player{ID: "4f7c0a2e-58c4-4a51-9d2e-3c1f0f1d2a3b", Name: "Ada"}) {
		t.Fatalf("player = %+v", player)
	}
	header, _, _ := strings.Cut(sign(t, set, claims(now)), ".")
	decoded, _ := base64.RawURLEncoding.DecodeString(header)
	if string(decoded) != `{"alg":"EdDSA","kid":"k1","typ":"JWT"}` {
		t.Fatalf("header = %s", decoded)
	}
}

func TestVerifyRefusesWhatTheLobbyDidNotIssueForThisGame(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	set := keySet(t, "k1:"+firstSeed)
	valid := sign(t, set, claims(now))
	otherIssuer := claims(now)
	otherIssuer.Issuer = "https://elsewhere.example.test"
	expired := claims(now.Add(-time.Hour))
	noSubject := claims(now)
	noSubject.Subject = ""
	headerAndClaims := valid[:strings.LastIndex(valid, ".")]
	public := keySet(t, "k1:"+firstSeed).JWKS().Keys[0].X
	hmacSigned := func(secret []byte) string {
		input := segment(t, map[string]string{"alg": "HS256", "kid": "k1", "typ": "JWT"}) + "." + segment(t, claims(now))
		mac := hmac.New(sha256.New, secret)
		mac.Write([]byte(input))
		return input + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	}
	publicBytes, _ := base64.RawURLEncoding.DecodeString(public)
	cases := map[string]struct{ token, audience string }{
		"another game":          {valid, "quiz-demo"},
		"no audience":           {valid, ""},
		"another issuer":        {sign(t, set, otherIssuer), "island-battles"},
		"expired":               {sign(t, set, expired), "island-battles"},
		"no subject":            {sign(t, set, noSubject), "island-battles"},
		"unknown kid":           {sign(t, keySet(t, "k9:"+firstSeed), claims(now)), "island-battles"},
		"another key's kid":     {sign(t, keySet(t, "k1:"+secondSeed), claims(now)), "island-battles"},
		"altered claims":        {segment(t, map[string]string{"alg": "EdDSA", "kid": "k1"}) + "." + segment(t, map[string]any{"iss": issuer, "sub": "someone-else", "aud": "island-battles", "exp": now.Add(time.Minute).Unix()}) + valid[strings.LastIndex(valid, "."):], "island-battles"},
		"alg none":              {segment(t, map[string]string{"alg": "none", "kid": "k1"}) + "." + segment(t, claims(now)) + ".", "island-battles"},
		"alg none, signed":      {segment(t, map[string]string{"alg": "none", "kid": "k1"}) + "." + valid[strings.Index(valid, ".")+1:], "island-battles"},
		"HS256 with public key": {hmacSigned(publicBytes), "island-battles"},
		"HS256 with x":          {hmacSigned([]byte(public)), "island-battles"},
		"no kid":                {segment(t, map[string]string{"alg": "EdDSA"}) + valid[strings.Index(valid, "."):], "island-battles"},
		"no signature":          {headerAndClaims, "island-battles"},
		"empty signature":       {headerAndClaims + ".", "island-battles"},
		"four segments":         {valid + ".x", "island-battles"},
		"not a token":           {"Bearer", "island-battles"},
		"oversized":             {valid + strings.Repeat("A", 5000), "island-battles"},
	}
	verifier := verifier(t, set, &now)
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := verifier.Verify(context.Background(), test.token, test.audience); !errors.Is(err, playertoken.ErrInvalid) {
				t.Fatalf("Verify() error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestATokenExpiresAfterItsLeeway(t *testing.T) {
	issued := time.Unix(1_800_000_000, 0)
	set := keySet(t, "k1:"+firstSeed)
	token := sign(t, set, claims(issued))
	expires := issued.Add(playertoken.MaxLifetime)
	for _, check := range []struct {
		at    time.Time
		valid bool
	}{
		{expires.Add(-time.Second), true},
		{expires.Add(playertoken.Leeway - time.Second), true},
		{expires.Add(playertoken.Leeway), false},
	} {
		now := check.at
		_, err := verifier(t, set, &now).Verify(context.Background(), token, "island-battles")
		if (err == nil) != check.valid {
			t.Fatalf("at exp%+v: Verify() error = %v, want valid %v", check.at.Sub(expires), err, check.valid)
		}
	}
}

func TestEveryKeyOfTheSetVerifiesAndTheFirstSigns(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	old := keySet(t, "old:"+firstSeed)
	rotated := keySet(t, " new:"+secondSeed+" , old:"+firstSeed+",")
	for _, token := range []string{sign(t, old, claims(now)), sign(t, rotated, claims(now))} {
		if _, err := verifier(t, rotated, &now).Verify(context.Background(), token, "island-battles"); err != nil {
			t.Fatal(err)
		}
	}
	jwks := rotated.JWKS()
	if len(jwks.Keys) != 2 || jwks.Keys[0].KeyID != "new" || jwks.Keys[1].KeyID != "old" {
		t.Fatalf("JWKS = %+v", jwks)
	}
	if key := jwks.Keys[0]; key.KeyType != "OKP" || key.Curve != "Ed25519" || key.Algorithm != "EdDSA" || key.Use != "sig" || len(key.X) != 43 {
		t.Fatalf("key = %+v", key)
	}
	// A key that was removed no longer verifies.
	if _, err := verifier(t, keySet(t, "new:"+secondSeed), &now).Verify(context.Background(), sign(t, old, claims(now)), "island-battles"); !errors.Is(err, playertoken.ErrInvalid) {
		t.Fatalf("Verify() error = %v, want ErrInvalid", err)
	}
}

func TestParseKeySetRefusesMalformedKeys(t *testing.T) {
	for _, value := range []string{
		"", " , ", "k1", ":" + firstSeed, "k 1:" + firstSeed, "k1:" + firstSeed + ",k1:" + secondSeed,
		"k1:short", "k1:" + firstSeed + "AAAA", "k1:" + firstSeed + "=",
	} {
		if _, err := playertoken.ParseKeySet(value); err == nil {
			t.Fatalf("ParseKeySet(%q) succeeded", value)
		}
	}
}

func TestFromAuthorizationReadsABearerCredential(t *testing.T) {
	for header, want := range map[string]string{"Bearer abc": "abc", "bearer  abc ": "abc", "Basic abc": "", "Bearer": "", "Bearer ": "", "": ""} {
		got, ok := playertoken.FromAuthorization(header)
		if got != want || ok != (want != "") {
			t.Fatalf("FromAuthorization(%q) = %q, %v", header, got, ok)
		}
	}
}

// jwksServer serves a key set's JWKS, which a test can replace, and counts
// the requests.
type jwksServer struct {
	*httptest.Server
	mutex        sync.Mutex
	set          *playertoken.KeySet
	cacheControl string
	status       int
	requests     int
}

func newJWKSServer(t *testing.T, set *playertoken.KeySet) *jwksServer {
	server := &jwksServer{set: set, cacheControl: "public, max-age=300", status: http.StatusOK}
	server.Server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		server.mutex.Lock()
		defer server.mutex.Unlock()
		server.requests++
		if server.status != http.StatusOK {
			response.WriteHeader(server.status)
			return
		}
		response.Header().Set("Cache-Control", server.cacheControl)
		_ = json.NewEncoder(response).Encode(server.set.JWKS())
	}))
	t.Cleanup(server.Close)
	return server
}

func (server *jwksServer) change(change func(*jwksServer)) {
	server.mutex.Lock()
	defer server.mutex.Unlock()
	change(server)
}

func (server *jwksServer) count() int {
	server.mutex.Lock()
	defer server.mutex.Unlock()
	return server.requests
}

func source(t *testing.T, server *jwksServer, now *time.Time) *playertoken.JWKSSource {
	t.Helper()
	source, err := playertoken.NewJWKSSource(server.URL+"/lobby/v1/jwks.json", playertoken.WithHTTPClient(server.Client()),
		playertoken.WithSourceClock(func() time.Time { return *now }))
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func TestTheJWKSSourceCachesTheKeysForTheirMaxAge(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	set := keySet(t, "k1:"+firstSeed)
	server := newJWKSServer(t, set)
	verifier := verifier(t, source(t, server, &now), &now)
	verify := func() {
		t.Helper()
		if _, err := verifier.Verify(context.Background(), sign(t, set, claims(now)), "island-battles"); err != nil {
			t.Fatal(err)
		}
	}
	verify()
	now = now.Add(4 * time.Minute)
	verify()
	if got := server.count(); got != 1 {
		t.Fatalf("requests within max-age = %d, want 1", got)
	}
	now = now.Add(time.Minute)
	verify()
	if got := server.count(); got != 2 {
		t.Fatalf("requests after max-age = %d, want 2", got)
	}
}

func TestTheJWKSSourceFetchesANewKeyAtMostOnceAMinute(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	server := newJWKSServer(t, keySet(t, "old:"+firstSeed))
	verifier := verifier(t, source(t, server, &now), &now)
	if _, err := verifier.Verify(context.Background(), sign(t, keySet(t, "old:"+firstSeed), claims(now)), "island-battles"); err != nil {
		t.Fatal(err)
	}
	// The lobby adds a key within a minute of the first fetch: the source
	// won't fetch again yet, so the new key's tokens are refused.
	rotated := keySet(t, "new:"+secondSeed+",old:"+firstSeed)
	server.change(func(server *jwksServer) { server.set = rotated })
	now = now.Add(30 * time.Second)
	for range 3 {
		if _, err := verifier.Verify(context.Background(), sign(t, rotated, claims(now)), "island-battles"); !errors.Is(err, playertoken.ErrInvalid) {
			t.Fatalf("Verify() error = %v, want ErrInvalid", err)
		}
	}
	if got := server.count(); got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}
	// A minute after the fetch, the unknown kid fetches the keys again.
	now = now.Add(30 * time.Second)
	if _, err := verifier.Verify(context.Background(), sign(t, rotated, claims(now)), "island-battles"); err != nil {
		t.Fatal(err)
	}
	if got := server.count(); got != 2 {
		t.Fatalf("requests = %d, want 2", got)
	}
	// A kid the lobby never had fetches once more, after another minute, and
	// is still refused.
	now = now.Add(time.Minute)
	for range 2 {
		if _, err := verifier.Verify(context.Background(), sign(t, keySet(t, "forged:"+firstSeed), claims(now)), "island-battles"); !errors.Is(err, playertoken.ErrInvalid) {
			t.Fatalf("Verify() error = %v, want ErrInvalid", err)
		}
	}
	if got := server.count(); got != 3 {
		t.Fatalf("requests = %d, want 3", got)
	}
}

func TestTheJWKSSourceKeepsItsKeysWhenTheLobbyIsDown(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	set := keySet(t, "k1:"+firstSeed)
	server := newJWKSServer(t, set)
	server.change(func(server *jwksServer) { server.status = http.StatusServiceUnavailable })
	verifier := verifier(t, source(t, server, &now), &now)
	// Without any keys, the failure is the lobby's, not the token's.
	_, err := verifier.Verify(context.Background(), sign(t, set, claims(now)), "island-battles")
	if err == nil || errors.Is(err, playertoken.ErrInvalid) {
		t.Fatalf("Verify() error = %v, want a fetch error", err)
	}
	server.change(func(server *jwksServer) { server.status = http.StatusOK })
	now = now.Add(time.Minute)
	if _, err := verifier.Verify(context.Background(), sign(t, set, claims(now)), "island-battles"); err != nil {
		t.Fatal(err)
	}
	server.change(func(server *jwksServer) { server.status = http.StatusServiceUnavailable })
	now = now.Add(10 * time.Minute)
	if _, err := verifier.Verify(context.Background(), sign(t, set, claims(now)), "island-battles"); err != nil {
		t.Fatalf("stale keys: %v", err)
	}
	if got := server.count(); got != 3 {
		t.Fatalf("requests = %d, want 3", got)
	}
}

func TestTheJWKSSourceReadsMaxAge(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	set := keySet(t, "k1:"+firstSeed)
	server := newJWKSServer(t, set)
	server.change(func(server *jwksServer) { server.cacheControl = "max-age=60" })
	verifier := verifier(t, source(t, server, &now), &now)
	for range 3 {
		if _, err := verifier.Verify(context.Background(), sign(t, set, claims(now)), "island-battles"); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Minute)
	}
	if got := server.count(); got != 3 {
		t.Fatalf("requests = %d, want 3", got)
	}
}
