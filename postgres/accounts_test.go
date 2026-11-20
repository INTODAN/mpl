package postgres_test

import (
	"context"
	"crypto/rand"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/INTODAN/mpl/domain"
	"github.com/INTODAN/mpl/internal/testutil"
	lobbypostgres "github.com/INTODAN/mpl/postgres"

	"github.com/google/uuid"
)

func TestAccountsHoldNothingOfGoogleButTheSubject(t *testing.T) {
	db := testutil.NewPostgresFixture(t)
	columnTypes, err := db.Migrator().ColumnTypes(&lobbypostgres.AccountRecord{})
	if err != nil {
		t.Fatal(err)
	}
	var columns []string
	for _, columnType := range columnTypes {
		columns = append(columns, columnType.Name())
	}
	slices.Sort(columns)
	want := []string{"created_at", "display_name", "display_name_key", "google_subject", "id", "updated_at"}
	if !slices.Equal(columns, want) {
		t.Fatalf("lobby_accounts columns = %v, want %v", columns, want)
	}
}

func TestDeletingAnAccountDeletesItsSessionsAndKeepsItsPlayers(t *testing.T) {
	db := testutil.NewPostgresFixture(t)
	store, err := lobbypostgres.NewIdentityStore(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	account := domain.Account{ID: uuid.NewString(), DisplayName: "Dana", CreatedAt: now}
	signIn := func(create bool) string {
		t.Helper()
		playerID := uuid.NewString()
		if err := store.CreateAccountSession(ctx, account, "subject", create,
			domain.Player{ID: playerID, DisplayName: account.DisplayName, AccountID: account.ID, CreatedAt: now},
			domain.Session{ID: uuid.NewString(), PlayerID: playerID, BearerHash: randomHash(t), CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
		); err != nil {
			t.Fatal(err)
		}
		return playerID
	}
	first, second := signIn(true), signIn(false)
	if err := store.CreateAccountSession(ctx, domain.Account{ID: uuid.NewString(), DisplayName: "Other", CreatedAt: now}, "subject", true,
		domain.Player{ID: uuid.NewString(), DisplayName: "Other", CreatedAt: now}, domain.Session{}); !errors.Is(err, domain.ErrAccountExists) {
		t.Fatalf("a second account of the subject = %v, want ErrAccountExists", err)
	}
	verification, err := store.LookupByPlayer(ctx, first)
	if err != nil || verification == nil || verification.Principal.AccountID != account.ID {
		t.Fatalf("LookupByPlayer = %+v, %v", verification, err)
	}

	guestID := uuid.NewString()
	if err := store.CreateGuest(ctx, domain.Player{ID: guestID, DisplayName: "Guest", CreatedAt: now},
		domain.Session{ID: uuid.NewString(), PlayerID: guestID, BearerHash: domain.CredentialHash{9}, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAccount(ctx, account.ID); err != nil {
		t.Fatal(err)
	}
	for _, playerID := range []string{first, second} {
		if verification, err := store.LookupByPlayer(ctx, playerID); err != nil || verification != nil {
			t.Fatalf("a deleted account's session = %+v, %v", verification, err)
		}
		var players []lobbypostgres.PlayerRecord
		if err := db.Where(&lobbypostgres.PlayerRecord{ID: playerID}).Find(&players).Error; err != nil {
			t.Fatal(err)
		}
		if len(players) != 1 || players[0].AccountID != nil {
			t.Fatalf("the player after deleting = %+v", players)
		}
	}
	if verification, err := store.LookupByPlayer(ctx, guestID); err != nil || verification == nil || verification.Principal.AccountID != "" {
		t.Fatalf("a guest after deleting another's account = %+v, %v", verification, err)
	}
	// A sign-in that adds a player to the deleted account finds it gone.
	if err := store.CreateAccountSession(ctx, account, "subject", false,
		domain.Player{ID: uuid.NewString(), DisplayName: account.DisplayName, AccountID: account.ID, CreatedAt: now}, domain.Session{}); !errors.Is(err, domain.ErrAccountGone) {
		t.Fatalf("a sign-in to the deleted account = %v, want ErrAccountGone", err)
	}
	if found, err := store.AccountBySubject(ctx, "subject"); err != nil || found != nil {
		t.Fatalf("the deleted account = %+v, %v", found, err)
	}
	if err := store.DeleteAccount(ctx, account.ID); !errors.Is(err, domain.ErrSignInRequired) {
		t.Fatalf("deleting it again = %v", err)
	}
	// An empty key finds no account, even while there are some.
	keptID, keptPlayer := uuid.NewString(), uuid.NewString()
	if err := store.CreateAccountSession(ctx, domain.Account{ID: keptID, DisplayName: "Kept", CreatedAt: now}, "kept", true,
		domain.Player{ID: keptPlayer, DisplayName: "Kept", AccountID: keptID, CreatedAt: now},
		domain.Session{ID: uuid.NewString(), PlayerID: keptPlayer, BearerHash: randomHash(t), CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	for name, lookup := range map[string]func(context.Context, string) (*domain.Account, error){"subject": store.AccountBySubject, "ID": store.AccountByID} {
		if found, err := lookup(ctx, ""); err != nil || found != nil {
			t.Fatalf("an account by an empty %s = %+v, %v", name, found, err)
		}
	}
}

func randomHash(t *testing.T) domain.CredentialHash {
	t.Helper()
	var hash domain.CredentialHash
	if _, err := rand.Read(hash[:]); err != nil {
		t.Fatal(err)
	}
	return hash
}
