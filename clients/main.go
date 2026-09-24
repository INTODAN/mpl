// Command lobby-example is a small manual client for the generic lobby API.
// It intentionally contains no server API key, signing secret, or proxy secret.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/centrifugal/centrifuge-go"
)

type guestSession struct {
	PlayerID    string `json:"player_id"`
	AccessToken string `json:"access_token"`
}
type connectionToken struct {
	Token        string `json:"token"`
	WebsocketURL string `json:"websocket_url"`
}
type lobbySnapshot struct {
	Lobby struct {
		ID       string `json:"id"`
		JoinCode string `json:"join_code"`
	} `json:"lobby"`
	MembershipGeneration int64  `json:"membership_generation"`
	Channel              string `json:"channel"`
}

func main() {
	apiURL := flag.String("api-url", "http://localhost:8888/lobby/v1", "public lobby API base URL")
	displayName := flag.String("name", "Guest", "guest display name (and default lobby name)")
	joinCode := flag.String("join-code", "", "join an existing lobby with this code")
	gameKey := flag.String("game-key", "island-battles", "registered game key")
	flag.Parse()

	rootContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	client := &http.Client{Timeout: 10 * time.Second}
	baseURL := strings.TrimRight(*apiURL, "/")

	guest, err := postJSON[guestSession](rootContext, client, baseURL+"/sessions", "", map[string]string{"display_name": *displayName})
	fatalIf(err)
	instanceID, err := newUUID()
	fatalIf(err)
	var snapshot lobbySnapshot
	if *joinCode != "" {
		snapshot, err = postJSON[lobbySnapshot](rootContext, client, baseURL+"/memberships", guest.AccessToken, map[string]string{"game_key": *gameKey, "join_code": *joinCode, "client_instance_id": instanceID})
	} else {
		requestID, uuidError := newUUID()
		fatalIf(uuidError)
		snapshot, err = postJSON[lobbySnapshot](rootContext, client, baseURL+"/lobbies", guest.AccessToken, map[string]any{
			"request_id": requestID, "game_key": *gameKey, "name": *displayName + "'s lobby", "capacity": 2,
			"teams": []map[string]any{{"name": "Team 1", "capacity": 1}, {"name": "Team 2", "capacity": 1}}, "client_instance_id": instanceID,
		})
	}
	fatalIf(err)
	encodedSnapshot, _ := json.MarshalIndent(snapshot, "", "  ")
	fmt.Printf("join code: %s\nchannel: %s\nsnapshot: %s\n", snapshot.Lobby.JoinCode, snapshot.Channel, encodedSnapshot)

	getConnectionToken := func() (connectionToken, error) {
		return postJSON[connectionToken](rootContext, client, baseURL+"/connection-token", guest.AccessToken, map[string]any{})
	}
	initialToken, err := getConnectionToken()
	fatalIf(err)
	var connected atomic.Bool
	realtimeClient := centrifuge.NewJsonClient(initialToken.WebsocketURL, centrifuge.Config{Token: initialToken.Token, GetToken: func(centrifuge.ConnectionTokenEvent) (string, error) {
		token, requestError := getConnectionToken()
		return token.Token, requestError
	}})
	defer realtimeClient.Close()
	realtimeClient.OnConnected(func(centrifuge.ConnectedEvent) { connected.Store(true) })
	realtimeClient.OnDisconnected(func(centrifuge.DisconnectedEvent) { connected.Store(false) })
	subscription, err := realtimeClient.NewSubscription(snapshot.Channel)
	fatalIf(err)
	invalidations := make(chan []byte, 32)
	go func() {
		for event := range invalidations {
			fmt.Printf("invalidation: %s\n", event)
		}
	}()
	subscription.OnPublication(func(event centrifuge.PublicationEvent) {
		select {
		case invalidations <- append([]byte(nil), event.Data...):
		default:
		}
	})
	if err := realtimeClient.Connect(); err != nil {
		fatalIf(err)
	}
	if err := subscription.Subscribe(); err != nil {
		fatalIf(err)
	}

	go heartbeatLoop(rootContext, client, baseURL, guest.AccessToken, *gameKey, snapshot.Lobby.ID, instanceID, snapshot.MembershipGeneration, &connected)
	<-rootContext.Done()
	cleanupContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := releaseLease(cleanupContext, client, baseURL, guest.AccessToken, *gameKey, snapshot.Lobby.ID, instanceID, snapshot.MembershipGeneration); err != nil {
		fmt.Fprintln(os.Stderr, "release lease:", err)
	}
}

func heartbeatLoop(ctx context.Context, client *http.Client, baseURL, token, gameKey, lobbyID, instanceID string, generation int64, connected *atomic.Bool) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !connected.Load() {
				continue
			}
			_, err := postJSON[struct{}](ctx, client, fmt.Sprintf("%s/games/%s/lobbies/%s/heartbeat", baseURL, url.PathEscape(gameKey), url.PathEscape(lobbyID)), token, map[string]any{"client_instance_id": instanceID, "membership_generation": generation})
			if err != nil {
				fmt.Fprintln(os.Stderr, "heartbeat:", err)
			}
		}
	}
}

func releaseLease(ctx context.Context, client *http.Client, baseURL, token, gameKey, lobbyID, instanceID string, generation int64) error {
	endpoint := fmt.Sprintf("%s/games/%s/lobbies/%s/leases/%s?generation=%d", baseURL, url.PathEscape(gameKey), url.PathEscape(lobbyID), url.PathEscape(instanceID), generation)
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(response.Body)
		return fmt.Errorf("release lease: %s: %s", response.Status, body)
	}
	return nil
}

func postJSON[T any](ctx context.Context, client *http.Client, endpoint, token string, input any) (T, error) {
	var result T
	body, err := json.Marshal(input)
	if err != nil {
		return result, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return result, err
	}
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(request)
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(response.Body)
		return result, fmt.Errorf("%s: %s", response.Status, responseBody)
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return result, err
	}
	return result, nil
}

func newUUID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	bytes[6] = bytes[6]&0x0f | 0x40
	bytes[8] = bytes[8]&0x3f | 0x80
	encoded := hex.EncodeToString(bytes)
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], nil
}
func fatalIf(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
