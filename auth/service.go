// Package auth issues and verifies generic lobby guest credentials.
// It performs no environment reads, database setup, or HTTP registration.
package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"islandBattles/lobby/domain"
)

// Service implements both the Authenticator and ConnectionTokenIssuer ports.
// Its dependencies are injected so bootstrap selects secrets and production I/O.
type Service struct {
	identities         domain.IdentityStore
	clock              domain.Clock
	policy             domain.Policy
	identifiers        domain.IdentifierSource
	signingSecret      []byte
	publicWebsocketURL string
}

// NewService validates all configuration required for guest credentials and
// Centrifugo connection JWTs. The secret is copied before retaining it.
func NewService(
	identities domain.IdentityStore,
	clock domain.Clock,
	policy domain.Policy,
	identifiers domain.IdentifierSource,
	signingSecret []byte,
	publicWebsocketURL string,
) (*Service, error) {
	if identities == nil || clock == nil || identifiers == nil {
		return nil, errors.New("lobby auth: missing dependency")
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	if len(signingSecret) == 0 {
		return nil, errors.New("lobby auth: empty JWT signing secret")
	}
	if strings.TrimSpace(publicWebsocketURL) == "" {
		return nil, errors.New("lobby auth: empty public websocket URL")
	}
	return &Service{
		identities: identities, clock: clock, policy: policy,
		identifiers: identifiers, signingSecret: append([]byte(nil), signingSecret...),
		publicWebsocketURL: publicWebsocketURL,
	}, nil
}

func (service *Service) CreateGuest(ctx context.Context, request domain.CreateGuestRequest) (domain.GuestSession, error) {
	if err := ctx.Err(); err != nil {
		return domain.GuestSession{}, unavailable(err)
	}
	displayName, err := domain.NormalizeName(request.DisplayName, 40)
	if err != nil {
		return domain.GuestSession{}, err
	}
	now := service.clock.Now().UTC()
	playerID, err := service.identifiers.UUID()
	if err != nil {
		return domain.GuestSession{}, unavailable(err)
	}
	sessionID, err := service.identifiers.UUID()
	if err != nil {
		return domain.GuestSession{}, unavailable(err)
	}
	if playerID == sessionID {
		return domain.GuestSession{}, unavailable(errors.New("identifier source reused a UUID"))
	}
	bearerBytes, err := service.identifiers.Bytes(32)
	if err != nil {
		return domain.GuestSession{}, unavailable(err)
	}
	if len(bearerBytes) < 32 {
		return domain.GuestSession{}, unavailable(errors.New("identifier source returned too few credential bytes"))
	}
	// Retain exactly 256 credential bits even when a custom source returns a
	// larger buffer. Authenticate enforces that same canonical size.
	bearer := encodeBearer(bearerBytes[:32])
	hash := hashBearer(bearer)
	expiresAt := now.Add(service.policy.SessionDuration).UTC()
	if err := service.identities.CreateGuest(ctx,
		domain.Player{ID: playerID, DisplayName: displayName, CreatedAt: now},
		domain.Session{ID: sessionID, PlayerID: playerID, BearerHash: hash, CreatedAt: now, ExpiresAt: expiresAt},
	); err != nil {
		return domain.GuestSession{}, unavailable(err)
	}
	return domain.GuestSession{PlayerID: playerID, AccessToken: bearer, ExpiresAt: expiresAt}, nil
}

func (service *Service) Authenticate(ctx context.Context, authorization string) (domain.SessionVerification, error) {
	if err := ctx.Err(); err != nil {
		return domain.SessionVerification{}, unavailable(err)
	}
	bearer, ok := parseBearer(authorization)
	if !ok {
		return domain.SessionVerification{}, domain.ErrUnauthenticated
	}
	verification, err := service.identities.LookupByBearerHash(ctx, hashBearer(bearer))
	if err != nil {
		return domain.SessionVerification{}, unavailable(err)
	}
	if verification == nil || !service.clock.Now().UTC().Before(verification.ExpiresAt.UTC()) {
		return domain.SessionVerification{}, domain.ErrUnauthenticated
	}
	return *verification, nil
}

func unavailable(cause error) error {
	return &domain.Error{Code: domain.CodeUnavailable, Message: domain.ErrUnavailable.Message, Cause: cause}
}

var _ domain.Authenticator = (*Service)(nil)
var _ domain.ConnectionTokenIssuer = (*Service)(nil)

// kept here to make UTC expiration handling obvious at the port boundary.
func minTime(left, right time.Time) time.Time {
	if left.Before(right) {
		return left.UTC()
	}
	return right.UTC()
}
