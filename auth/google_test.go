package auth_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/INTODAN/mpl/auth"
	"github.com/INTODAN/mpl/auth/googletest"
	"github.com/INTODAN/mpl/domain"
)

type movingClock struct{ now time.Time }

func (clock *movingClock) Now() time.Time { return clock.now }

const testClientID = "client-1.apps.googleusercontent.com"

func newTestVerifier(t *testing.T, maxAge time.Duration) (*auth.GoogleVerifier, *googletest.Issuer, *movingClock, *atomic.Int32) {
	t.Helper()
	issuer, err := googletest.NewIssuer("key-1")
	if err != nil {
		t.Fatal(err)
	}
	var fetches atomic.Int32
	keys := issuer.Handler(maxAge)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		fetches.Add(1)
		keys.ServeHTTP(response, request)
	}))
	t.Cleanup(server.Close)
	clock := &movingClock{now: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)}
	verifier, err := auth.NewGoogleVerifier([]string{"other-client", testClientID}, server.URL, server.Client(), clock)
	if err != nil {
		t.Fatal(err)
	}
	return verifier, issuer, clock, &fetches
}

func TestGoogleVerifierReturnsTheSubjectOfAValidToken(t *testing.T) {
	verifier, issuer, clock, _ := newTestVerifier(t, time.Hour)
	for _, issuerName := range []string{"accounts.google.com", "https://accounts.google.com"} {
		token, err := issuer.Token(googletest.Claims{Issuer: issuerName, Audience: testClientID, Subject: "1234", ExpiresAt: clock.now.Add(time.Minute),
			Extra: map[string]any{"email": "dana@example.org", "name": "Dana"}})
		if err != nil {
			t.Fatal(err)
		}
		subject, err := verifier.Verify(context.Background(), token)
		if err != nil || subject != "1234" {
			t.Fatalf("Verify(iss %s) = %q, %v; want 1234", issuerName, subject, err)
		}
	}
}

func TestGoogleVerifierRefusesInvalidTokens(t *testing.T) {
	verifier, issuer, clock, _ := newTestVerifier(t, time.Hour)
	forgedKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	valid := googletest.Claims{Issuer: "https://accounts.google.com", Audience: testClientID, Subject: "1234", ExpiresAt: clock.now.Add(time.Minute)}
	with := func(change func(*googletest.Claims)) googletest.Claims {
		claims := valid
		change(&claims)
		return claims
	}
	sign := func(claims googletest.Claims) string {
		token, err := issuer.Token(claims)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	forged, err := issuer.SignWith(forgedKey, valid)
	if err != nil {
		t.Fatal(err)
	}
	other, err := googletest.NewIssuer("key-2")
	if err != nil {
		t.Fatal(err)
	}
	unknownKey, err := other.Token(valid)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"wrong signature": forged,
		"unknown key":     unknownKey,
		"wrong issuer":    sign(with(func(claims *googletest.Claims) { claims.Issuer = "https://evil.example.org" })),
		"wrong audience":  sign(with(func(claims *googletest.Claims) { claims.Audience = "someone-else" })),
		"expired":         sign(with(func(claims *googletest.Claims) { claims.ExpiresAt = clock.now })),
		"no subject":      sign(with(func(claims *googletest.Claims) { claims.Subject = "" })),
		"not a JWT":       "not-a-token",
		"empty":           "",
	}
	for name, token := range cases {
		if subject, err := verifier.Verify(context.Background(), token); !errors.Is(err, domain.ErrUnauthenticated) {
			t.Errorf("%s: Verify = %q, %v; want unauthenticated", name, subject, err)
		}
	}
}

func TestGoogleVerifierCachesKeysForTheirMaxAge(t *testing.T) {
	verifier, issuer, clock, fetches := newTestVerifier(t, 10*time.Minute)
	verify := func() {
		t.Helper()
		token, err := issuer.GoogleToken("1234", testClientID, clock.now)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := verifier.Verify(context.Background(), token); err != nil {
			t.Fatal(err)
		}
	}
	verify()
	clock.now = clock.now.Add(9 * time.Minute)
	verify()
	if got := fetches.Load(); got != 1 {
		t.Fatalf("fetches within max-age = %d, want 1", got)
	}
	clock.now = clock.now.Add(2 * time.Minute)
	verify()
	if got := fetches.Load(); got != 2 {
		t.Fatalf("fetches after max-age = %d, want 2", got)
	}
}

func TestGoogleVerifierFetchesAgainForAnUnknownKeyAtMostOnceAMinute(t *testing.T) {
	verifier, _, clock, fetches := newTestVerifier(t, time.Hour)
	other, err := googletest.NewIssuer("key-2")
	if err != nil {
		t.Fatal(err)
	}
	token, err := other.GoogleToken("1234", testClientID, clock.now)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := verifier.Verify(context.Background(), token); !errors.Is(err, domain.ErrUnauthenticated) {
			t.Fatalf("Verify = %v, want unauthenticated", err)
		}
	}
	if got := fetches.Load(); got != 1 {
		t.Fatalf("fetches = %d, want 1", got)
	}
	clock.now = clock.now.Add(2 * time.Minute)
	if _, err := verifier.Verify(context.Background(), token); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("Verify = %v, want unauthenticated", err)
	}
	if got := fetches.Load(); got != 2 {
		t.Fatalf("fetches after a minute = %d, want 2", got)
	}
}

func TestGoogleVerifierReportsUnreachableKeysAsUnavailable(t *testing.T) {
	var fetches atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		fetches.Add(1)
		response.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	clock := &movingClock{now: time.Now()}
	verifier, err := auth.NewGoogleVerifier([]string{testClientID}, server.URL, server.Client(), clock)
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := googletest.NewIssuer("key-1")
	if err != nil {
		t.Fatal(err)
	}
	token, err := issuer.GoogleToken("1234", testClientID, clock.now)
	if err != nil {
		t.Fatal(err)
	}
	// A failed fetch isn't tried again for a minute, whatever kid tokens name.
	for range 3 {
		if _, err := verifier.Verify(context.Background(), token); domain.ErrorCodeOf(err) != domain.CodeUnavailable {
			t.Fatalf("Verify = %v, want unavailable", err)
		}
	}
	if got := fetches.Load(); got != 1 {
		t.Fatalf("fetches = %d, want 1", got)
	}
	clock.now = clock.now.Add(2 * time.Minute)
	if _, err := verifier.Verify(context.Background(), token); domain.ErrorCodeOf(err) != domain.CodeUnavailable || fetches.Load() != 2 {
		t.Fatalf("Verify a minute later = %v after %d fetches", err, fetches.Load())
	}
}

func TestGoogleVerifierRefusesOtherAlgorithmsAndAudienceLists(t *testing.T) {
	verifier, issuer, clock, _ := newTestVerifier(t, time.Hour)
	valid, err := issuer.GoogleToken("1234", testClientID, clock.now)
	if err != nil {
		t.Fatal(err)
	}
	_, payload, _ := strings.Cut(valid, ".")
	payload, signature, _ := strings.Cut(payload, ".")
	for _, header := range []string{`{"alg":"none","kid":"key-1"}`, `{"alg":"HS256","kid":"key-1"}`, `{"alg":"RS256"}`} {
		forged := base64.RawURLEncoding.EncodeToString([]byte(header)) + "." + payload + "." + signature
		if _, err := verifier.Verify(context.Background(), forged); !errors.Is(err, domain.ErrUnauthenticated) {
			t.Errorf("header %s: Verify = %v, want unauthenticated", header, err)
		}
	}
	listed, err := issuer.Token(googletest.Claims{Issuer: "accounts.google.com", Subject: "1234", ExpiresAt: clock.now.Add(time.Minute),
		Extra: map[string]any{"aud": []string{testClientID}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(context.Background(), listed); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("an audience list: Verify = %v, want unauthenticated", err)
	}
}
