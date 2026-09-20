package auth_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"islandBattles/lobby"
	"islandBattles/lobby/auth"
	"islandBattles/lobby/internal/testutil"
	lobbypostgres "islandBattles/lobby/postgres"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const signingSecret = "identity-test-signing-secret"

type testClock struct{ now time.Time }

func (clock *testClock) Now() time.Time { return clock.now }

type failingIdentifiers struct{}

func (failingIdentifiers) UUID() (string, error) { return "", errors.New("random source unavailable") }
func (failingIdentifiers) JoinCode() (string, error) {
	return "", errors.New("random source unavailable")
}
func (failingIdentifiers) Bytes(int) ([]byte, error) {
	return nil, errors.New("random source unavailable")
}

func TestGuestCreationStoresOnlyCredentialHash(t *testing.T) {
	service, _, db, _ := newService(t)
	created, err := service.CreateGuest(context.Background(), lobby.CreateGuestRequest{DisplayName: "  Ada  "})
	if err != nil {
		t.Fatal(err)
	}
	verified, err := service.Authenticate(context.Background(), "Bearer "+created.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Principal.PlayerID != created.PlayerID || verified.Principal.DisplayName != "Ada" {
		t.Fatalf("verified principal = %#v", verified.Principal)
	}
	var session lobbypostgres.SessionRecord
	if err := db.Where(&lobbypostgres.SessionRecord{PlayerID: created.PlayerID}).First(&session).Error; err != nil {
		t.Fatal(err)
	}
	if session.ID == created.PlayerID || strings.Contains(string(session.BearerHash), created.AccessToken) || string(session.BearerHash) == created.AccessToken {
		t.Fatal("stored session contains raw credential or shares player identity")
	}
	if len(session.BearerHash) != sha256.Size {
		t.Fatalf("stored hash length = %d", len(session.BearerHash))
	}
}

func TestGuestsWithSameDisplayNameRemainDistinct(t *testing.T) {
	service, _, _, _ := newService(t)
	first, err := service.CreateGuest(context.Background(), lobby.CreateGuestRequest{DisplayName: "Ada"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.CreateGuest(context.Background(), lobby.CreateGuestRequest{DisplayName: "Ada"})
	if err != nil {
		t.Fatal(err)
	}
	if first.PlayerID == second.PlayerID || first.AccessToken == second.AccessToken {
		t.Fatal("same display name reused an identity or credential")
	}
}

func TestGuestCreationIsAtomic(t *testing.T) {
	_, identities, db, clock := newService(t)
	playerID, sessionID := uuid.NewString(), uuid.NewString()
	err := identities.CreateGuest(context.Background(),
		lobby.Player{ID: playerID, DisplayName: "Ada", CreatedAt: clock.now},
		// The session refers to another player, forcing its FK insert to fail.
		lobby.Session{ID: sessionID, PlayerID: uuid.NewString(), BearerHash: lobby.CredentialHash{1}, CreatedAt: clock.now, ExpiresAt: clock.now.Add(time.Hour)},
	)
	if err == nil {
		t.Fatal("forced session insertion failure succeeded")
	}
	var count int64
	if err := db.Model(&lobbypostgres.PlayerRecord{}).Where(&lobbypostgres.PlayerRecord{ID: playerID}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("failed guest creation left an orphan player")
	}
}

func TestGuestCreationRandomnessFailureHasNoWrites(t *testing.T) {
	_, identities, db, clock := newService(t)
	service, err := auth.NewService(identities, clock, lobby.DefaultPolicy("island-battles"), failingIdentifiers{}, []byte(signingSecret), "wss://public.example")
	if err != nil {
		t.Fatal(err)
	}
	if created, err := service.CreateGuest(context.Background(), lobby.CreateGuestRequest{DisplayName: "Ada"}); !errors.Is(err, lobby.ErrUnavailable) || created.AccessToken != "" {
		t.Fatalf("randomness failure result = %#v, %v", created, err)
	}
	var count int64
	if err := db.Model(&lobbypostgres.PlayerRecord{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("randomness failure wrote a player")
	}
}

func TestGuestHasExactlyOneSession(t *testing.T) {
	service, _, db, clock := newService(t)
	guest, err := service.CreateGuest(context.Background(), lobby.CreateGuestRequest{DisplayName: "Ada"})
	if err != nil {
		t.Fatal(err)
	}
	duplicate := lobbypostgres.SessionRecord{ID: uuid.NewString(), PlayerID: guest.PlayerID, BearerHash: make([]byte, 32), CreatedAt: clock.now, ExpiresAt: clock.now.Add(time.Hour)}
	if err := db.Create(&duplicate).Error; err == nil {
		t.Fatal("second session for one player succeeded")
	}
	first, err := service.Authenticate(context.Background(), "Bearer "+guest.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Authenticate(context.Background(), "Bearer "+guest.AccessToken)
	if err != nil || first.Principal != second.Principal {
		t.Fatalf("shared bearer authentication = %#v, %v", second, err)
	}
}

func TestAuthenticateRejectsInvalidCredentials(t *testing.T) {
	service, _, db, clock := newService(t)
	guest, err := service.CreateGuest(context.Background(), lobby.CreateGuestRequest{DisplayName: "Ada"})
	if err != nil {
		t.Fatal(err)
	}
	unknown := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	expired := guest.AccessToken
	if err := db.Model(&lobbypostgres.SessionRecord{}).Where(&lobbypostgres.SessionRecord{PlayerID: guest.PlayerID}).Update("expires_at", clock.now).Error; err != nil {
		t.Fatal(err)
	}
	for _, header := range []string{"", "Bearer ", "bearer " + guest.AccessToken, "Bearer not-base64!", "Bearer " + unknown, "Bearer " + expired} {
		if _, err := service.Authenticate(context.Background(), header); !errors.Is(err, lobby.ErrUnauthenticated) {
			t.Errorf("Authenticate(%q) error = %v, want unauthenticated", header, err)
		}
	}
}

func TestAuthenticateIgnoresClaimedIdentity(t *testing.T) {
	service, _, _, _ := newService(t)
	first, err := service.CreateGuest(context.Background(), lobby.CreateGuestRequest{DisplayName: "Ada"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.CreateGuest(context.Background(), lobby.CreateGuestRequest{DisplayName: "Grace"})
	if err != nil {
		t.Fatal(err)
	}
	verified, err := service.Authenticate(context.Background(), "Bearer "+first.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Principal.PlayerID != first.PlayerID || verified.Principal.PlayerID == second.PlayerID || verified.Principal.PlayerID == "0" {
		t.Fatalf("bearer identity was not authoritative: %#v", verified.Principal)
	}
}

func TestConnectionTokenClaimsAndExpiry(t *testing.T) {
	service, _, db, clock := newService(t)
	guest, err := service.CreateGuest(context.Background(), lobby.CreateGuestRequest{DisplayName: "Ada"})
	if err != nil {
		t.Fatal(err)
	}
	sessionExpiry := clock.now.Add(2 * time.Minute)
	if err := db.Model(&lobbypostgres.SessionRecord{}).Where(&lobbypostgres.SessionRecord{PlayerID: guest.PlayerID}).Update("expires_at", sessionExpiry).Error; err != nil {
		t.Fatal(err)
	}
	verification, err := service.Authenticate(context.Background(), "Bearer "+guest.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	token, err := service.IssueConnectionToken(context.Background(), verification)
	if err != nil {
		t.Fatal(err)
	}
	claims := verifyJWT(t, token.Token, []byte(signingSecret))
	if claims.Subject != guest.PlayerID || claims.Expires != sessionExpiry.Unix() || claims.IssuedAt != clock.now.Unix() || token.ExpiresAt != sessionExpiry || token.WebsocketURL != "wss://lobby.example.test/connection/websocket" {
		t.Fatalf("connection token claims = %#v, response = %#v", claims, token)
	}
	if strings.Contains(token.Token, signingSecret) {
		t.Fatal("connection token contains signing secret")
	}
}

func TestConnectionTokenRefreshHasNoLobbySideEffects(t *testing.T) {
	service, _, db, _ := newService(t)
	guest, err := service.CreateGuest(context.Background(), lobby.CreateGuestRequest{DisplayName: "Ada"})
	if err != nil {
		t.Fatal(err)
	}
	verification, err := service.Authenticate(context.Background(), "Bearer "+guest.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	before := lobbyCounts(t, db)
	first, err := service.IssueConnectionToken(context.Background(), verification)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.IssueConnectionToken(context.Background(), verification)
	if err != nil {
		t.Fatal(err)
	}
	if first.Token == "" || second.Token == "" || first.Token != second.Token {
		t.Fatal("fixed clock should produce valid deterministic refresh tokens")
	}
	if after := lobbyCounts(t, db); before != after {
		t.Fatalf("connection refresh changed lobby rows: %v -> %v", before, after)
	}
}

func TestConnectionTokenFailsClosed(t *testing.T) {
	_, identities, _, clock := newService(t)
	policy := lobby.DefaultPolicy("island-battles")
	if _, err := auth.NewService(identities, clock, policy, lobby.CryptoIdentifiers{}, nil, "wss://public.example"); err == nil {
		t.Fatal("empty signing secret configured successfully")
	}
	service, _, db, _ := newService(t)
	guest, err := service.CreateGuest(context.Background(), lobby.CreateGuestRequest{DisplayName: "Ada"})
	if err != nil {
		t.Fatal(err)
	}
	verification, err := service.Authenticate(context.Background(), "Bearer "+guest.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if token, err := service.IssueConnectionToken(canceled, verification); !errors.Is(err, lobby.ErrUnavailable) || token.Token != "" {
		t.Fatalf("cancelled issuance = %#v, %v", token, err)
	}
	if err := db.Model(&lobbypostgres.SessionRecord{}).Where(&lobbypostgres.SessionRecord{PlayerID: guest.PlayerID}).Update("expires_at", clock.now).Error; err != nil {
		t.Fatal(err)
	}
	if token, err := service.IssueConnectionToken(context.Background(), verification); !errors.Is(err, lobby.ErrUnauthenticated) || token.Token != "" {
		t.Fatalf("expired issuance = %#v, %v", token, err)
	}
}

func TestLookupByPlayerHonorsSessionExpiry(t *testing.T) {
	service, identities, db, clock := newService(t)
	guest, err := service.CreateGuest(context.Background(), lobby.CreateGuestRequest{DisplayName: "Ada"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&lobbypostgres.SessionRecord{}).Where(&lobbypostgres.SessionRecord{PlayerID: guest.PlayerID}).Update("expires_at", clock.now).Error; err != nil {
		t.Fatal(err)
	}
	verification, err := identities.LookupByPlayer(context.Background(), guest.PlayerID)
	if err != nil || verification == nil || !verification.ExpiresAt.Equal(clock.now) {
		t.Fatalf("LookupByPlayer = %#v, %v", verification, err)
	}
	if _, err := service.Authenticate(context.Background(), "Bearer "+guest.AccessToken); !errors.Is(err, lobby.ErrUnauthenticated) {
		t.Fatalf("exact expiry Authenticate error = %v", err)
	}
}

func newService(t *testing.T) (*auth.Service, *lobbypostgres.IdentityStore, *gorm.DB, *testClock) {
	t.Helper()
	db := testutil.NewPostgresFixture(t)
	identities, err := lobbypostgres.NewIdentityStore(db)
	if err != nil {
		t.Fatal(err)
	}
	clock := &testClock{now: time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)}
	service, err := auth.NewService(identities, clock, lobby.DefaultPolicy("island-battles"), lobby.CryptoIdentifiers{}, []byte(signingSecret), "wss://lobby.example.test/connection/websocket")
	if err != nil {
		t.Fatal(err)
	}
	return service, identities, db, clock
}

func verifyJWT(t *testing.T, token string, secret []byte) struct {
	Subject  string `json:"sub"`
	IssuedAt int64  `json:"iat"`
	Expires  int64  `json:"exp"`
} {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT part count = %d", len(parts))
	}
	signer := hmac.New(sha256.New, secret)
	_, _ = signer.Write([]byte(parts[0] + "." + parts[1]))
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(signature, signer.Sum(nil)) {
		t.Fatal("invalid JWT signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims struct {
		Subject  string `json:"sub"`
		IssuedAt int64  `json:"iat"`
		Expires  int64  `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	return claims
}

func lobbyCounts(t *testing.T, db *gorm.DB) [4]int64 {
	t.Helper()
	var counts [4]int64
	for index, model := range []any{&lobbypostgres.RoomRecord{}, &lobbypostgres.MembershipRecord{}, &lobbypostgres.LeaseRecord{}, &lobbypostgres.PlayerRecord{}} {
		if err := db.Model(model).Count(&counts[index]).Error; err != nil {
			t.Fatal(err)
		}
	}
	return counts
}
