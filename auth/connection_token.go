package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"

	"islandBattles/lobby/domain"
)

type jwtHeader struct {
	Algorithm string `json:"alg"`
	Type      string `json:"typ"`
}

type jwtClaims struct {
	Subject  string `json:"sub"`
	IssuedAt int64  `json:"iat"`
	Expires  int64  `json:"exp"`
}

// IssueConnectionToken rechecks the durable one-session identity before signing
// a short connection JWT. This prevents an arbitrary constructed verification
// value from becoming authority and creates no lobby-side state.
func (service *Service) IssueConnectionToken(ctx context.Context, verification domain.SessionVerification) (domain.ConnectionToken, error) {
	if err := ctx.Err(); err != nil {
		return domain.ConnectionToken{}, unavailable(err)
	}
	if domain.ValidateUUID(verification.Principal.PlayerID) != nil || domain.ValidateUUID(verification.Principal.SessionID) != nil {
		return domain.ConnectionToken{}, domain.ErrUnauthenticated
	}
	stored, err := service.identities.LookupByPlayer(ctx, verification.Principal.PlayerID)
	if err != nil {
		return domain.ConnectionToken{}, unavailable(err)
	}
	now := service.clock.Now().UTC()
	if stored == nil || !now.Before(stored.ExpiresAt.UTC()) ||
		stored.Principal.PlayerID != verification.Principal.PlayerID ||
		subtle.ConstantTimeCompare([]byte(stored.Principal.SessionID), []byte(verification.Principal.SessionID)) != 1 {
		return domain.ConnectionToken{}, domain.ErrUnauthenticated
	}
	if err := ctx.Err(); err != nil {
		return domain.ConnectionToken{}, unavailable(err)
	}
	expiresAt := minTime(now.Add(service.policy.ConnectionTokenDuration), stored.ExpiresAt)
	token, err := service.signConnectionJWT(stored.Principal.PlayerID, now.Unix(), expiresAt.Unix())
	if err != nil {
		return domain.ConnectionToken{}, unavailable(err)
	}
	return domain.ConnectionToken{Token: token, ExpiresAt: expiresAt, WebsocketURL: service.publicWebsocketURL}, nil
}

func (service *Service) signConnectionJWT(subject string, issuedAt, expiresAt int64) (string, error) {
	header, err := json.Marshal(jwtHeader{Algorithm: "HS256", Type: "JWT"})
	if err != nil {
		return "", err
	}
	claims, err := json.Marshal(jwtClaims{Subject: subject, IssuedAt: issuedAt, Expires: expiresAt})
	if err != nil {
		return "", err
	}
	encodedHeader := base64.RawURLEncoding.EncodeToString(header)
	encodedClaims := base64.RawURLEncoding.EncodeToString(claims)
	signingInput := encodedHeader + "." + encodedClaims
	signer := hmac.New(sha256.New, service.signingSecret)
	if _, err := signer.Write([]byte(signingInput)); err != nil {
		return "", err
	}
	signature := base64.RawURLEncoding.EncodeToString(signer.Sum(nil))
	return signingInput + "." + signature, nil
}
