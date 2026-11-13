package gamehooks_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"islandBattles/lobby/gamehooks"
)

var secret = []byte(strings.Repeat("s", gamehooks.MinSecretBytes))

func TestASignatureVerifiesOnlyTheRequestItSigned(t *testing.T) {
	at := time.Unix(1_800_000_000, 0)
	header := gamehooks.Sign(secret, at, "POST", "/hooks/start", []byte(`{"a":1}`))
	if !strings.HasPrefix(header, "t=1800000000,v1=") || len(header) != len("t=1800000000,v1=")+64 {
		t.Fatalf("header = %q", header)
	}
	if err := gamehooks.Verify(secret, header, at, "POST", "/hooks/start", []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	for name, verify := range map[string]func() error{
		"another body": func() error { return gamehooks.Verify(secret, header, at, "POST", "/hooks/start", []byte(`{"a":2}`)) },
		"another path": func() error {
			return gamehooks.Verify(secret, header, at, "POST", "/hooks/ready-check", []byte(`{"a":1}`))
		},
		"another method": func() error { return gamehooks.Verify(secret, header, at, "PUT", "/hooks/start", []byte(`{"a":1}`)) },
		"another secret": func() error {
			return gamehooks.Verify([]byte(strings.Repeat("o", 32)), header, at, "POST", "/hooks/start", []byte(`{"a":1}`))
		},
		"no secret": func() error { return gamehooks.Verify(nil, header, at, "POST", "/hooks/start", []byte(`{"a":1}`)) },
		"another time": func() error {
			return gamehooks.Verify(secret, strings.Replace(header, "t=1800000000", "t=1800000001", 1), at, "POST", "/hooks/start", []byte(`{"a":1}`))
		},
		"no header": func() error { return gamehooks.Verify(secret, "", at, "POST", "/hooks/start", []byte(`{"a":1}`)) },
		"no time": func() error {
			return gamehooks.Verify(secret, header[strings.Index(header, "v1="):], at, "POST", "/hooks/start", []byte(`{"a":1}`))
		},
		"no hmac": func() error {
			return gamehooks.Verify(secret, "t=1800000000", at, "POST", "/hooks/start", []byte(`{"a":1}`))
		},
		"not hex": func() error {
			return gamehooks.Verify(secret, "t=1800000000,v1=zz", at, "POST", "/hooks/start", []byte(`{"a":1}`))
		},
		"malformed": func() error {
			return gamehooks.Verify(secret, "garbage", at, "POST", "/hooks/start", []byte(`{"a":1}`))
		},
		"too old": func() error {
			return gamehooks.Verify(secret, header, at.Add(5*time.Minute+time.Second), "POST", "/hooks/start", []byte(`{"a":1}`))
		},
		"from too soon": func() error {
			return gamehooks.Verify(secret, header, at.Add(-5*time.Minute-time.Second), "POST", "/hooks/start", []byte(`{"a":1}`))
		},
	} {
		if err := verify(); !errors.Is(err, gamehooks.ErrBadSignature) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Up to five minutes of skew either way is accepted.
	for _, skew := range []time.Duration{5 * time.Minute, -5 * time.Minute} {
		if err := gamehooks.Verify(secret, header, at.Add(skew), "POST", "/hooks/start", []byte(`{"a":1}`)); err != nil {
			t.Errorf("skew %s: %v", skew, err)
		}
	}
}

// The signed message is t + "." + method + " " + path + "." + body, as the
// lobby README documents for games written in other languages. The expected
// value is openssl's:
//
//	printf '%s' '1700000000.POST /game/lobby-hooks/start.{}' | openssl dgst -sha256 -hmac 0123456789abcdef0123456789abcdef
func TestTheSignatureIsTheDocumentedHMAC(t *testing.T) {
	header := gamehooks.Sign([]byte("0123456789abcdef0123456789abcdef"), time.Unix(1700000000, 0), "POST", "/game/lobby-hooks/start", []byte(`{}`))
	if want := "t=1700000000,v1=3c6505b8849a6df2536beae76ee23be92057394a296d73decbd889a150d20c5d"; header != want {
		t.Fatalf("header = %q, want %q", header, want)
	}
}
