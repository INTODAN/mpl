package postgres_test

import (
	"context"
	"testing"
	"time"

	"islandBattles/lobby"
	"islandBattles/lobby/internal/testutil"
	lobbypostgres "islandBattles/lobby/postgres"

	"github.com/google/uuid"
)

func TestIdentityStoreLookupByPlayerReturnsStoredIdentity(t *testing.T) {
	db := testutil.NewPostgresFixture(t)
	store, err := lobbypostgres.NewIdentityStore(db)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	playerID, sessionID := uuid.NewString(), uuid.NewString()
	if err := store.CreateGuest(context.Background(), lobby.Player{ID: playerID, DisplayName: "Stored Name", CreatedAt: now}, lobby.Session{ID: sessionID, PlayerID: playerID, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	verification, err := store.LookupByPlayer(context.Background(), playerID)
	if err != nil || verification == nil {
		t.Fatalf("LookupByPlayer = %#v, %v", verification, err)
	}
	if verification.Principal.PlayerID != playerID || verification.Principal.SessionID != sessionID || verification.Principal.DisplayName != "Stored Name" || !verification.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("unexpected verification: %#v", verification)
	}
}

func TestIdentityStoreCreateGuestRollsBackPlayerOnSessionFailure(t *testing.T) {
	db := testutil.NewPostgresFixture(t)
	store, err := lobbypostgres.NewIdentityStore(db)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	playerID := uuid.NewString()
	err = store.CreateGuest(context.Background(), lobby.Player{ID: playerID, DisplayName: "Ada", CreatedAt: now}, lobby.Session{ID: uuid.NewString(), PlayerID: uuid.NewString(), CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
	if err == nil {
		t.Fatal("session foreign key failure unexpectedly succeeded")
	}
	var count int64
	if err := db.Model(&lobbypostgres.PlayerRecord{}).Where(&lobbypostgres.PlayerRecord{ID: playerID}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("player was committed even though session creation failed")
	}
}
