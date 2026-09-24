package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"strings"

	"islandBattles/lobby/domain"
)

func encodeBearer(random []byte) string {
	return base64.RawURLEncoding.EncodeToString(random)
}

func hashBearer(bearer string) domain.CredentialHash {
	return sha256.Sum256([]byte(bearer))
}

// parseBearer accepts only the exact Bearer scheme and canonical base64url
// credentials produced by CreateGuest. It rejects folded whitespace and padded
// or alternate encodings before any storage lookup.
func parseBearer(authorization string) (string, bool) {
	if !strings.HasPrefix(authorization, "Bearer ") {
		return "", false
	}
	bearer := strings.TrimPrefix(authorization, "Bearer ")
	if bearer == "" || strings.ContainsAny(bearer, " \t\r\n") {
		return "", false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(bearer)
	if err != nil || len(decoded) != 32 {
		return "", false
	}
	canonical := encodeBearer(decoded)
	if subtle.ConstantTimeCompare([]byte(canonical), []byte(bearer)) != 1 {
		return "", false
	}
	return bearer, true
}
