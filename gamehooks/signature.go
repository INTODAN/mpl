package gamehooks

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

// SignatureHeader carries a request's signature, in both directions: on the
// lobby's hook calls to a game, and on a game's calls to the game API.
const SignatureHeader = "Lobby-Signature"

// MaxClockSkew is how far a signature's time may be from the receiver's
// clock, either way, before the receiver refuses it as a replay.
const MaxClockSkew = 5 * time.Minute

// MinSecretBytes is the shortest shared secret a game may have.
const MinSecretBytes = 32

// ErrBadSignature refuses a request whose signature is missing, malformed,
// too old or too new, or made with another secret. A receiver answers it with
// 401.
var ErrBadSignature = errors.New("the request's Lobby-Signature doesn't verify")

// Sign returns the Lobby-Signature header value of a request:
// "t=<unix seconds>,v1=<hex HMAC-SHA256>". The HMAC, with the game's shared
// secret as its key, covers the time, the method, the request path (without
// the query) and the body, as t + "." + method + " " + path + "." + body.
func Sign(secret []byte, at time.Time, method, path string, body []byte) string {
	timestamp := strconv.FormatInt(at.Unix(), 10)
	return "t=" + timestamp + ",v1=" + hex.EncodeToString(mac(secret, timestamp, method, path, body))
}

// Verify checks a Lobby-Signature header value against the request it came
// with, at now. It fails with ErrBadSignature when the header is missing or
// malformed, its time is more than MaxClockSkew from now, or its HMAC
// differs. The HMACs are compared in constant time.
func Verify(secret []byte, header string, now time.Time, method, path string, body []byte) error {
	if len(secret) == 0 {
		return ErrBadSignature
	}
	var timestamp, signature string
	for part := range strings.SplitSeq(header, ",") {
		name, value, found := strings.Cut(strings.TrimSpace(part), "=")
		if !found {
			return ErrBadSignature
		}
		switch name {
		case "t":
			timestamp = value
		case "v1":
			signature = value
		}
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return ErrBadSignature
	}
	if skew := now.Sub(time.Unix(seconds, 0)); skew > MaxClockSkew || skew < -MaxClockSkew {
		return ErrBadSignature
	}
	received, err := hex.DecodeString(signature)
	if err != nil {
		return ErrBadSignature
	}
	if !hmac.Equal(received, mac(secret, timestamp, method, path, body)) {
		return ErrBadSignature
	}
	return nil
}

func mac(secret []byte, timestamp, method, path string, body []byte) []byte {
	hash := hmac.New(sha256.New, secret)
	hash.Write([]byte(timestamp + "." + method + " " + path + "."))
	hash.Write(body)
	return hash.Sum(nil)
}
