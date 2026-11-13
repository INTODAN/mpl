package auth_test

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"islandBattles/lobby/auth"
	"islandBattles/lobby/domain"
	"islandBattles/lobby/playertoken"
)

const playerID = "4f7c0a2e-58c4-4a51-9d2e-3c1f0f1d2a3b"

func playerTokens(t *testing.T, clock domain.Clock, lifetime time.Duration) (*auth.PlayerTokens, *playertoken.KeySet) {
	t.Helper()
	keys, err := playertoken.ParseKeySet("k1:" + base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("s", 32))))
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := auth.NewPlayerTokens(keys, "https://lobby.example.test", lifetime, clock,
		domain.DefaultPolicy(domain.GameRegistration{Key: "island-battles"}, domain.GameRegistration{Key: "quiz-demo"}))
	if err != nil {
		t.Fatal(err)
	}
	return tokens, keys
}

func session(expires time.Time) domain.SessionVerification {
	return domain.SessionVerification{Principal: domain.Principal{PlayerID: playerID, SessionID: playerID, DisplayName: "Ada"}, ExpiresAt: expires}
}

func TestAPlayerTokenNamesItsPlayerAndGameAndLastsFifteenMinutes(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 11, 12, 10, 0, 0, 500_000_000, time.UTC)}
	tokens, keys := playerTokens(t, clock, playertoken.MaxLifetime)
	issued, err := tokens.IssuePlayerToken(context.Background(), session(clock.now.Add(24*time.Hour)), "island-battles")
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 11, 12, 10, 15, 0, 0, time.UTC); !issued.ExpiresAt.Equal(want) {
		t.Fatalf("expires_at = %v, want %v", issued.ExpiresAt, want)
	}
	verifier, err := playertoken.NewVerifier("https://lobby.example.test", keys, playertoken.WithVerifierClock(func() time.Time { return clock.now }))
	if err != nil {
		t.Fatal(err)
	}
	player, err := verifier.Verify(context.Background(), issued.Token, "island-battles")
	if err != nil || player != (playertoken.Player{ID: playerID, Name: "Ada"}) {
		t.Fatalf("Verify() = %+v, %v", player, err)
	}
	if _, err := verifier.Verify(context.Background(), issued.Token, "quiz-demo"); !errors.Is(err, playertoken.ErrInvalid) {
		t.Fatalf("Verify() for another game = %v", err)
	}
	if jwks := tokens.PublicKeys(); len(jwks.Keys) != 1 || jwks.Keys[0].KeyID != "k1" {
		t.Fatalf("PublicKeys() = %+v", jwks)
	}
}

func TestAPlayerTokenNeverOutlivesItsSession(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)}
	tokens, keys := playerTokens(t, clock, playertoken.MaxLifetime)
	sessionExpires := clock.now.Add(5*time.Minute + 700*time.Millisecond)
	issued, err := tokens.IssuePlayerToken(context.Background(), session(sessionExpires), "island-battles")
	if err != nil {
		t.Fatal(err)
	}
	if want := clock.now.Add(5 * time.Minute); !issued.ExpiresAt.Equal(want) {
		t.Fatalf("expires_at = %v, want %v", issued.ExpiresAt, want)
	}
	// The verifier's leeway is the only time the token outlasts the session.
	at := sessionExpires.Add(playertoken.Leeway)
	verifier, err := playertoken.NewVerifier("https://lobby.example.test", keys, playertoken.WithVerifierClock(func() time.Time { return at }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(context.Background(), issued.Token, "island-battles"); !errors.Is(err, playertoken.ErrInvalid) {
		t.Fatalf("Verify() after the session = %v, want ErrInvalid", err)
	}
	for _, expires := range []time.Time{clock.now, clock.now.Add(500 * time.Millisecond)} {
		if _, err := tokens.IssuePlayerToken(context.Background(), session(expires), "island-battles"); domain.ErrorCodeOf(err) != domain.CodeUnauthenticated {
			t.Fatalf("session ending at %v: error = %v, want unauthenticated", expires, err)
		}
	}
}

func TestAPlayerTokenIsOnlyForARegisteredGame(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)}
	tokens, _ := playerTokens(t, clock, playertoken.MaxLifetime)
	for _, gameKey := range []string{"chess", "", "Island-Battles"} {
		if _, err := tokens.IssuePlayerToken(context.Background(), session(clock.now.Add(time.Hour)), gameKey); domain.ErrorCodeOf(err) != domain.CodeNotFound {
			t.Fatalf("%q: error = %v, want not_found", gameKey, err)
		}
	}
}

func TestAPlayerTokenLifetimeIsShortened(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)}
	tokens, _ := playerTokens(t, clock, time.Minute)
	issued, err := tokens.IssuePlayerToken(context.Background(), session(clock.now.Add(time.Hour)), "quiz-demo")
	if err != nil || !issued.ExpiresAt.Equal(clock.now.Add(time.Minute)) {
		t.Fatalf("IssuePlayerToken() = %+v, %v", issued, err)
	}
	for _, lifetime := range []time.Duration{0, -time.Minute, playertoken.MaxLifetime + time.Second} {
		keys, _ := playertoken.ParseKeySet("k1:" + base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("s", 32))))
		if _, err := auth.NewPlayerTokens(keys, "https://lobby.example.test", lifetime, clock, domain.DefaultPolicy(domain.GameRegistration{Key: "quiz-demo"})); err == nil {
			t.Fatalf("lifetime %v accepted", lifetime)
		}
	}
}
