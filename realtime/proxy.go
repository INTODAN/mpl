package realtime

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"islandBattles/lobby"
)

const SubscribePath = "/internal/lobby/centrifugo/subscribe"
const RefreshPath = "/internal/lobby/centrifugo/sub-refresh"
const ProxySecretHeader = "X-Lobby-Proxy-Secret"

type Proxy struct {
	authorizer lobby.SubscriptionAuthorizer
	secret     string
}

func NewProxy(authorizer lobby.SubscriptionAuthorizer, secret string) (*Proxy, error) {
	if authorizer == nil || secret == "" {
		return nil, lobby.ErrInvalidInput
	}
	return &Proxy{authorizer: authorizer, secret: secret}, nil
}

// Register deliberately bypasses public Huma registration: these are trusted
// Centrifugo protocol endpoints, not player command endpoints.
func (proxy *Proxy) Register(router chi.Router) {
	router.Post(SubscribePath, proxy.ServeHTTP)
	router.Post(RefreshPath, proxy.ServeHTTP)
}

func (proxy *Proxy) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Content-Type", "application/json")
	if request.Method != http.MethodPost {
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if request.URL.Path != SubscribePath && request.URL.Path != RefreshPath {
		http.NotFound(response, request)
		return
	}
	if subtle.ConstantTimeCompare([]byte(request.Header.Get(ProxySecretHeader)), []byte(proxy.secret)) != 1 {
		http.Error(response, "unauthorized", http.StatusUnauthorized)
		return
	}
	var input struct {
		Client  string `json:"client"`
		User    string `json:"user"`
		Channel string `json:"channel"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 64<<10))
	if err := decoder.Decode(&input); err != nil || input.Client == "" || input.User == "" || input.Channel == "" {
		http.Error(response, "invalid proxy request", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		http.Error(response, "invalid proxy request", http.StatusBadRequest)
		return
	}
	authorization, err := proxy.authorizer.AuthorizeSubscription(request.Context(), input.User, input.Channel)
	if err != nil {
		if !errors.Is(err, lobby.ErrPermissionDenied) && !errors.Is(err, lobby.ErrUnauthenticated) && !errors.Is(err, lobby.ErrNotFound) {
			http.Error(response, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		if request.URL.Path == RefreshPath {
			_, _ = io.WriteString(response, `{"result":{"expired":true}}`)
			return
		}
		_, _ = io.WriteString(response, `{"error":{"code":403,"message":"permission denied"}}`)
		return
	}
	_ = json.NewEncoder(response).Encode(struct {
		Result struct {
			ExpireAt int64 `json:"expire_at"`
		} `json:"result"`
	}{Result: struct {
		ExpireAt int64 `json:"expire_at"`
	}{ExpireAt: authorization.AllowedUntil.Unix()}})
}
