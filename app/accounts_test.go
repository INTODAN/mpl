package app_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/INTODAN/mpl/app"
	"github.com/INTODAN/mpl/auth/googletest"
	"github.com/INTODAN/mpl/domain"
	"github.com/INTODAN/mpl/playertoken"

	"github.com/google/uuid"
)

const googleClientID = "lobby-test.apps.googleusercontent.com"

// signInServer serves the lobby with sign-in on, verifying ID tokens that
// issuer signs.
func signInServer(t *testing.T) (*server, *googletest.Issuer) {
	lobby, issuer, _ := signInServerWithGame(t)
	return lobby, issuer
}

func signInServerWithGame(t *testing.T) (*server, *googletest.Issuer, *readingGame) {
	t.Helper()
	issuer, err := googletest.NewIssuer("test-key")
	if err != nil {
		t.Fatal(err)
	}
	keys := httptest.NewServer(issuer.Handler(time.Hour))
	t.Cleanup(keys.Close)
	lobby, game := newServer(t, 4, func(environment map[string]string) {
		environment[app.GoogleClientIDsEnv] = googleClientID + ", second-client"
		environment[app.GoogleCertsURLEnv] = keys.URL
	})
	return lobby, issuer, game
}

func googleToken(t *testing.T, issuer *googletest.Issuer, subject string) string {
	t.Helper()
	token, err := issuer.GoogleToken(subject, googleClientID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return token
}

type accountSession struct {
	PlayerID    string         `json:"player_id"`
	AccessToken string         `json:"access_token"`
	ExpiresAt   time.Time      `json:"expires_at"`
	Account     domain.Account `json:"account"`
}

type problem struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

func TestSignInIsOffWithoutGoogleClientIDs(t *testing.T) {
	lobby, _ := newServer(t, 2)
	var settings map[string]any
	lobby.call(http.MethodGet, "/lobby/v1/sign-in", "", nil, http.StatusOK, &settings)
	if value, present := settings["google_client_id"]; !present || value != nil {
		t.Fatalf("sign-in settings = %v, want google_client_id null", settings)
	}
	lobby.call(http.MethodPost, "/lobby/v1/sessions/google", "", map[string]string{"credential": "token", "display_name": "Dana"}, http.StatusNotFound, nil)
	guest := lobby.guest("Guest")
	var refused problem
	lobby.call(http.MethodGet, "/lobby/v1/account", guest.AccessToken, nil, http.StatusForbidden, &refused)
	if refused.Code != "sign_in_required" {
		t.Fatalf("a guest's account = %+v", refused)
	}
}

func TestSignInWithGoogleCreatesAnAccountAndOpensSessions(t *testing.T) {
	lobby, issuer := signInServer(t)
	var settings map[string]any
	lobby.call(http.MethodGet, "/lobby/v1/sign-in", "", nil, http.StatusOK, &settings)
	if settings["google_client_id"] != googleClientID {
		t.Fatalf("sign-in settings = %v", settings)
	}
	credential := googleToken(t, issuer, "google-subject-1")

	// A new account needs a display name, and the client posts the same
	// credential again with one.
	var refused problem
	lobby.call(http.MethodPost, "/lobby/v1/sessions/google", "", map[string]string{"credential": credential}, http.StatusUnprocessableEntity, &refused)
	if refused.Code != "display_name_required" {
		t.Fatalf("a new account without a name = %+v", refused)
	}
	lobby.call(http.MethodPost, "/lobby/v1/sessions/google", "", map[string]string{"credential": credential, "display_name": "   "}, http.StatusUnprocessableEntity, &refused)
	if refused.Code != "display_name_required" {
		t.Fatalf("a new account with a blank name = %+v", refused)
	}
	lobby.call(http.MethodPost, "/lobby/v1/sessions/google", "", map[string]string{"credential": credential, "display_name": "D!"}, http.StatusUnprocessableEntity, &refused)
	if refused.Code != "invalid_input" || refused.Detail != domain.ErrInvalidAccountName.Message {
		t.Fatalf("an invalid name = %+v", refused)
	}
	var first accountSession
	lobby.call(http.MethodPost, "/lobby/v1/sessions/google", "", map[string]string{"credential": credential, "display_name": "  Dana Kraken "}, http.StatusCreated, &first)
	if first.Account.DisplayName != "Dana Kraken" || first.Account.ID == "" || first.PlayerID == "" || first.AccessToken == "" ||
		time.Until(first.ExpiresAt) < 23*time.Hour {
		t.Fatalf("first sign-in = %+v", first)
	}

	// The session works on a guest's routes: it creates a lobby, and its
	// player has the account's name.
	var room domain.Snapshot
	lobby.call(http.MethodPost, "/lobby/v1/lobbies", first.AccessToken, map[string]any{"request_id": uuid.NewString(), "game_key": "mock-game", "name": "Signed in",
		"teams": []map[string]string{{"name": "Red"}, {"name": "Blue"}}, "client_instance_id": uuid.NewString()}, http.StatusCreated, &room)
	if room.Members[0].DisplayName != "Dana Kraken" || room.Members[0].PlayerID != first.PlayerID {
		t.Fatalf("members = %+v", room.Members)
	}
	lobby.call(http.MethodPost, "/lobby/v1/player-tokens", first.AccessToken, map[string]string{"game_key": "mock-game"}, http.StatusOK, nil)

	// A second sign-in of the same Google account, another device, is the
	// same account and a new player; it ignores display_name.
	var second accountSession
	lobby.call(http.MethodPost, "/lobby/v1/sessions/google", "", map[string]string{"credential": googleToken(t, issuer, "google-subject-1"), "display_name": "Ignored"}, http.StatusCreated, &second)
	if second.Account != first.Account || second.PlayerID == first.PlayerID || second.AccessToken == first.AccessToken {
		t.Fatalf("second sign-in = %+v, first = %+v", second, first)
	}
	var account domain.Account
	lobby.call(http.MethodGet, "/lobby/v1/account", second.AccessToken, nil, http.StatusOK, &account)
	if account.ID != first.Account.ID || account.DisplayName != "Dana Kraken" {
		t.Fatalf("account = %+v", account)
	}
}

func TestSignInRefusesInvalidIDTokens(t *testing.T) {
	lobby, issuer := signInServer(t)
	expired, err := issuer.Token(googletest.Claims{Issuer: "accounts.google.com", Audience: googleClientID, Subject: "1", ExpiresAt: time.Now().Add(-time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	wrongAudience, err := issuer.GoogleToken("1", "someone-else", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for name, credential := range map[string]string{"expired": expired, "wrong audience": wrongAudience, "garbage": "a.b.c"} {
		var refused problem
		lobby.call(http.MethodPost, "/lobby/v1/sessions/google", "", map[string]string{"credential": credential, "display_name": "Dana"}, http.StatusUnauthorized, &refused)
		if refused.Code != "unauthenticated" {
			t.Fatalf("%s: %+v", name, refused)
		}
	}
	// The second configured client ID is accepted too.
	second, err := issuer.GoogleToken("1", "second-client", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	lobby.call(http.MethodPost, "/lobby/v1/sessions/google", "", map[string]string{"credential": second, "display_name": "Dana"}, http.StatusCreated, nil)
}

func TestAccountNamesAreUniqueWithoutRegardToCase(t *testing.T) {
	lobby, issuer := signInServer(t)
	var dana accountSession
	lobby.call(http.MethodPost, "/lobby/v1/sessions/google", "", map[string]string{"credential": googleToken(t, issuer, "dana"), "display_name": "Dana"}, http.StatusCreated, &dana)
	var refused problem
	lobby.call(http.MethodPost, "/lobby/v1/sessions/google", "", map[string]string{"credential": googleToken(t, issuer, "other"), "display_name": "DANA"}, http.StatusConflict, &refused)
	if refused.Code != "display_name_taken" {
		t.Fatalf("a taken name = %+v", refused)
	}
	var other accountSession
	lobby.call(http.MethodPost, "/lobby/v1/sessions/google", "", map[string]string{"credential": googleToken(t, issuer, "other"), "display_name": "Kraken"}, http.StatusCreated, &other)
	lobby.call(http.MethodPut, "/lobby/v1/account", other.AccessToken, map[string]string{"display_name": "dAnA"}, http.StatusConflict, &refused)
	if refused.Code != "display_name_taken" {
		t.Fatalf("a rename to a taken name = %+v", refused)
	}
	// A guest may use any name, an account's too.
	lobby.guest("Dana")

	// Renaming changes the account, and the caller's player for lobbies it
	// creates or joins from now on.
	var renamed domain.Account
	lobby.call(http.MethodPut, "/lobby/v1/account", dana.AccessToken, map[string]string{"display_name": "Dana the Great"}, http.StatusOK, &renamed)
	if renamed.ID != dana.Account.ID || renamed.DisplayName != "Dana the Great" {
		t.Fatalf("renamed = %+v", renamed)
	}
	var room domain.Snapshot
	lobby.call(http.MethodPost, "/lobby/v1/lobbies", dana.AccessToken, map[string]any{"request_id": uuid.NewString(), "game_key": "mock-game", "name": "Renamed",
		"teams": []map[string]string{{"name": "Red"}, {"name": "Blue"}}, "client_instance_id": uuid.NewString()}, http.StatusCreated, &room)
	if room.Members[0].DisplayName != "Dana the Great" {
		t.Fatalf("member = %+v", room.Members[0])
	}
	// The old name is free again, and case alone is a rename too.
	lobby.call(http.MethodPut, "/lobby/v1/account", other.AccessToken, map[string]string{"display_name": "dana"}, http.StatusOK, nil)
	lobby.call(http.MethodPut, "/lobby/v1/account", other.AccessToken, map[string]string{"display_name": "Dana"}, http.StatusOK, nil)
}

func TestDeletingAnAccountEndsEverySessionOfIt(t *testing.T) {
	lobby, issuer := signInServer(t)
	var first, second accountSession
	lobby.call(http.MethodPost, "/lobby/v1/sessions/google", "", map[string]string{"credential": googleToken(t, issuer, "dana"), "display_name": "Dana"}, http.StatusCreated, &first)
	lobby.call(http.MethodPost, "/lobby/v1/sessions/google", "", map[string]string{"credential": googleToken(t, issuer, "dana")}, http.StatusCreated, &second)
	guest := lobby.guest("Guest")
	lobby.call(http.MethodDelete, "/lobby/v1/account", guest.AccessToken, nil, http.StatusForbidden, nil)

	lobby.call(http.MethodDelete, "/lobby/v1/account", first.AccessToken, nil, http.StatusNoContent, nil)
	for _, session := range []accountSession{first, second} {
		lobby.call(http.MethodGet, "/lobby/v1/account", session.AccessToken, nil, http.StatusUnauthorized, nil)
		lobby.call(http.MethodPost, "/lobby/v1/player-tokens", session.AccessToken, map[string]string{"game_key": "mock-game"}, http.StatusUnauthorized, nil)
	}
	lobby.call(http.MethodPost, "/lobby/v1/player-tokens", guest.AccessToken, map[string]string{"game_key": "mock-game"}, http.StatusOK, nil)

	// The same Google account signs in again as a new account, and may take
	// the name again.
	var refused problem
	lobby.call(http.MethodPost, "/lobby/v1/sessions/google", "", map[string]string{"credential": googleToken(t, issuer, "dana")}, http.StatusUnprocessableEntity, &refused)
	if refused.Code != "display_name_required" {
		t.Fatalf("signing in after deleting = %+v", refused)
	}
	var again accountSession
	lobby.call(http.MethodPost, "/lobby/v1/sessions/google", "", map[string]string{"credential": googleToken(t, issuer, "dana"), "display_name": "Dana"}, http.StatusCreated, &again)
	if again.Account.ID == first.Account.ID {
		t.Fatalf("the new account reused the deleted account's ID %s", again.Account.ID)
	}
}

// A game learns which account each member is, without reading the lobby's
// tables: from the player token, the hooks and the snapshots it reads and
// is started with. Players see only whether a member is signed in.
func TestGamesLearnWhichAccountEachMemberIs(t *testing.T) {
	lobby, issuer, game := signInServerWithGame(t)
	var dana accountSession
	lobby.call(http.MethodPost, "/lobby/v1/sessions/google", "", map[string]string{"credential": googleToken(t, issuer, "dana"), "display_name": "Dana"}, http.StatusCreated, &dana)
	guest := lobby.guest("Guest")

	// The player token.
	var token domain.PlayerToken
	lobby.call(http.MethodPost, "/lobby/v1/player-tokens", dana.AccessToken, map[string]string{"game_key": "mock-game"}, http.StatusOK, &token)
	keys, err := playertoken.NewJWKSSource(lobby.url + playertoken.JWKSPath)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := playertoken.NewVerifier("http://localhost:8890", keys)
	if err != nil {
		t.Fatal(err)
	}
	if player, err := verifier.Verify(context.Background(), token.Token, "mock-game"); err != nil || player.AccountID != dana.Account.ID || player.ID != dana.PlayerID {
		t.Fatalf("a signed-in player's token = %+v, %v", player, err)
	}
	lobby.call(http.MethodPost, "/lobby/v1/player-tokens", guest.AccessToken, map[string]string{"game_key": "mock-game"}, http.StatusOK, &token)
	if player, err := verifier.Verify(context.Background(), token.Token, "mock-game"); err != nil || player.AccountID != "" {
		t.Fatalf("a guest's token = %+v, %v", player, err)
	}

	// The player API's snapshot says who is signed in, and nothing more.
	var room struct {
		Lobby   domain.Lobby
		Teams   []domain.Team
		Members []map[string]any
	}
	lobby.call(http.MethodPost, "/lobby/v1/lobbies", dana.AccessToken, map[string]any{"request_id": uuid.NewString(), "game_key": "mock-game", "name": "Accounts",
		"teams": []map[string]string{{"name": "Red"}, {"name": "Blue"}}, "client_instance_id": uuid.NewString()}, http.StatusCreated, &room)
	lobby.call(http.MethodPost, "/lobby/v1/memberships", guest.AccessToken, map[string]any{"game_key": "mock-game", "join_code": room.Lobby.JoinCode,
		"client_instance_id": uuid.NewString()}, http.StatusOK, &room)
	for _, member := range room.Members {
		if _, present := member["account_id"]; present {
			t.Fatalf("the player API shows an account ID: %v", member)
		}
		if signedIn := member["signed_in"]; signedIn != (member["player_id"] == dana.PlayerID) {
			t.Fatalf("member %v: signed_in = %v", member["display_name"], signedIn)
		}
	}

	// The member settings hook names the member who sets them.
	path := fmt.Sprintf("/lobby/v1/games/mock-game/lobbies/%s", room.Lobby.ID)
	lobby.call(http.MethodPut, path+"/member-settings", dana.AccessToken, map[string]any{"member_settings": map[string]any{}}, http.StatusOK, nil)
	if game.settingsMember != (domain.MemberIdentity{PlayerID: dana.PlayerID, AccountID: dana.Account.ID, DisplayName: "Dana"}) {
		t.Fatalf("validate-member-settings got %+v", game.settingsMember)
	}
	lobby.call(http.MethodPut, path+"/member-settings", guest.AccessToken, map[string]any{"member_settings": map[string]any{}}, http.StatusOK, nil)
	if game.settingsMember != (domain.MemberIdentity{PlayerID: guest.PlayerID, DisplayName: "Guest"}) {
		t.Fatalf("validate-member-settings got %+v for a guest", game.settingsMember)
	}

	// The ready check, the start's snapshot and the game API's snapshot.
	accountOf := map[string]string{dana.PlayerID: dana.Account.ID, guest.PlayerID: ""}
	for index, member := range []string{dana.AccessToken, guest.AccessToken} {
		lobby.call(http.MethodPut, path+"/team", member, map[string]any{"team_id": room.Teams[index].ID}, http.StatusOK, nil)
		lobby.call(http.MethodPut, path+"/ready", member, map[string]any{"ready": true}, http.StatusOK, nil)
	}
	lobby.call(http.MethodPost, path+"/start", dana.AccessToken, map[string]any{}, http.StatusOK, nil)
	if len(game.readyMembers) != 2 {
		t.Fatalf("the start's ready check got %+v", game.readyMembers)
	}
	for _, member := range game.readyMembers {
		if member.AccountID != accountOf[member.PlayerID] {
			t.Fatalf("ready-check member %+v", member)
		}
	}
	read, err := game.lobby.Snapshot(context.Background(), room.Lobby.ID)
	if err != nil {
		t.Fatal(err)
	}
	for name, snapshot := range map[string]domain.Snapshot{"start": game.started, "game API": read} {
		if len(snapshot.Members) != 2 {
			t.Fatalf("%s snapshot members = %+v", name, snapshot.Members)
		}
		for _, member := range snapshot.Members {
			got := ""
			if member.AccountID != nil {
				got = *member.AccountID
			}
			if got != accountOf[member.PlayerID] {
				t.Fatalf("%s snapshot: %s has account %q", name, member.DisplayName, got)
			}
		}
	}
}
