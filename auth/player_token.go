package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"islandBattles/lobby/domain"
	"islandBattles/lobby/playertoken"
)

// PlayerTokens issues the player tokens that games verify with the lobby's
// public keys, and publishes those keys.
type PlayerTokens struct {
	keys     *playertoken.KeySet
	issuer   string
	lifetime time.Duration
	clock    domain.Clock
	policy   domain.Policy
}

// NewPlayerTokens signs with keys as issuer, the lobby's public URL, for the
// games of policy. A token lasts lifetime, at most playertoken.MaxLifetime,
// and never past its session.
func NewPlayerTokens(keys *playertoken.KeySet, issuer string, lifetime time.Duration, clock domain.Clock, policy domain.Policy) (*PlayerTokens, error) {
	if keys == nil || clock == nil {
		return nil, errors.New("lobby auth: missing player token dependency")
	}
	if strings.TrimSpace(issuer) == "" {
		return nil, errors.New("lobby auth: empty player token issuer")
	}
	if lifetime <= 0 || lifetime > playertoken.MaxLifetime {
		return nil, errors.New("lobby auth: a player token lasts more than 0 and at most 15 minutes")
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	return &PlayerTokens{keys: keys, issuer: issuer, lifetime: lifetime, clock: clock, policy: policy}, nil
}

// IssuePlayerToken signs a token for the session's player and gameKey. The
// token expires after the lifetime or with the session, whichever comes
// first. An unknown game is ErrNotFound.
func (tokens *PlayerTokens) IssuePlayerToken(ctx context.Context, verification domain.SessionVerification, gameKey string) (domain.PlayerToken, error) {
	if err := ctx.Err(); err != nil {
		return domain.PlayerToken{}, unavailable(err)
	}
	if _, err := tokens.policy.Game(gameKey); err != nil {
		return domain.PlayerToken{}, domain.ErrNotFound
	}
	if domain.ValidateUUID(verification.Principal.PlayerID) != nil {
		return domain.PlayerToken{}, domain.ErrUnauthenticated
	}
	now := tokens.clock.Now().UTC()
	// exp is in whole seconds, so it is rounded down, never past the session.
	expires := minTime(now.Add(tokens.lifetime), verification.ExpiresAt).Truncate(time.Second)
	if !now.Before(expires) {
		return domain.PlayerToken{}, domain.ErrUnauthenticated
	}
	token, err := tokens.keys.Sign(playertoken.Claims{
		Issuer: tokens.issuer, Subject: verification.Principal.PlayerID, Audience: gameKey,
		IssuedAt: now.Unix(), Expires: expires.Unix(), Name: verification.Principal.DisplayName,
	})
	if err != nil {
		return domain.PlayerToken{}, unavailable(err)
	}
	return domain.PlayerToken{Token: token, ExpiresAt: expires}, nil
}

// PublicKeys is the JWKS of every key: the one that signs, and any previous
// ones.
func (tokens *PlayerTokens) PublicKeys() playertoken.JWKS { return tokens.keys.JWKS() }

var _ domain.PlayerTokenIssuer = (*PlayerTokens)(nil)
