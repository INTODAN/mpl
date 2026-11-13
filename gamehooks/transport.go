package gamehooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"islandBattles/lobby/domain"
)

// DefaultTimeout bounds each hook call and each game API request.
const DefaultTimeout = 5 * time.Second

// Option changes how a Client or a GameAPIClient calls.
type Option func(*transport)

// WithTimeout replaces DefaultTimeout.
func WithTimeout(timeout time.Duration) Option {
	return func(transport *transport) { transport.timeout = timeout }
}

// WithHTTPClient replaces http.DefaultClient. The client's own timeout, if
// any, applies as well.
func WithHTTPClient(client *http.Client) Option {
	return func(transport *transport) { transport.client = client }
}

// WithClock replaces the clock that dates signatures.
func WithClock(now func() time.Time) Option {
	return func(transport *transport) { transport.now = now }
}

// transport signs and sends JSON requests to one base URL.
type transport struct {
	base    *url.URL
	secret  []byte
	client  *http.Client
	timeout time.Duration
	now     func() time.Time
}

func newTransport(base string, secret []byte, options []Option) (*transport, error) {
	parsed, err := ParseServiceURL(base)
	if err != nil {
		return nil, err
	}
	if len(secret) < MinSecretBytes {
		return nil, fmt.Errorf("the shared secret needs at least %d bytes", MinSecretBytes)
	}
	result := &transport{base: parsed, secret: bytes.Clone(secret), client: http.DefaultClient, timeout: DefaultTimeout, now: time.Now}
	for _, option := range options {
		option(result)
	}
	if result.timeout <= 0 || result.client == nil || result.now == nil {
		return nil, errors.New("invalid game hooks option")
	}
	return result, nil
}

// ParseServiceURL accepts an absolute URL without a query or fragment: https,
// or http for a loopback host such as localhost or 127.0.0.1, which local
// development and tests use. A trailing slash is dropped.
func ParseServiceURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("%q is not an absolute URL without a query", raw)
	}
	switch parsed.Scheme {
	case "https":
	case "http":
		host := parsed.Hostname()
		if address := net.ParseIP(host); host != "localhost" && (address == nil || !address.IsLoopback()) {
			return nil, fmt.Errorf("%q must use https, unless its host is a loopback address", raw)
		}
	default:
		return nil, fmt.Errorf("%q must use https", raw)
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	parsed.RawPath = ""
	return parsed, nil
}

// reply is a hook's or the game API's answer: its status and its body, which
// a refusal decodes as a Problem.
type reply struct {
	status int
	body   []byte
}

func (answer reply) problem() Problem {
	var problem Problem
	_ = json.Unmarshal(answer.body, &problem)
	problem.Status = answer.status
	return problem
}

// send signs and sends a request with a JSON body, or none when request is
// nil, to the base URL's path joined with path, and reads the answer. A
// transport failure, a timeout or an unreadable answer is unavailable; the
// caller decides about the status. When ctx itself ends, send returns its
// error.
func (transport *transport) send(ctx context.Context, method string, path []string, request any) (reply, error) {
	var body []byte
	if request != nil {
		var err error
		if body, err = json.Marshal(request); err != nil {
			return reply{}, err
		}
	}
	target := transport.base.JoinPath(path...)
	callContext, cancel := context.WithTimeout(ctx, transport.timeout)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(callContext, method, target.String(), bytes.NewReader(body))
	if err != nil {
		return reply{}, unavailable(err)
	}
	if request != nil {
		httpRequest.Header.Set("Content-Type", "application/json")
	}
	httpRequest.Header.Set("Accept", "application/json, "+ProblemContentType)
	httpRequest.Header.Set(SignatureHeader, Sign(transport.secret, transport.now(), method, httpRequest.URL.EscapedPath(), body))
	response, err := transport.client.Do(httpRequest)
	if err != nil {
		if ctx.Err() != nil {
			return reply{}, ctx.Err()
		}
		return reply{}, unavailable(err)
	}
	defer func() { _ = response.Body.Close() }()
	answer, err := io.ReadAll(io.LimitReader(response.Body, MaxBodyBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return reply{}, ctx.Err()
		}
		return reply{}, unavailable(err)
	}
	if len(answer) > MaxBodyBytes {
		return reply{}, unavailable(errors.New("the answer is too large"))
	}
	return reply{status: response.StatusCode, body: answer}, nil
}

// unavailable reports a call that could not be answered. Its cause is kept as
// text, so that a timeout of the call itself is never mistaken for the end of
// the caller's own context.
func unavailable(cause error) error {
	return &domain.Error{Code: domain.CodeUnavailable, Message: domain.ErrUnavailable.Message, Cause: errors.New(cause.Error())}
}

// unexpected reports an answer that the protocol doesn't allow.
func unexpected(what string, answer reply) error {
	return unavailable(fmt.Errorf("%s answered %d", what, answer.status))
}

// decode reads a 200 answer's JSON body into into.
func decode(what string, answer reply, into any) error {
	if err := json.Unmarshal(answer.body, into); err != nil {
		return unavailable(fmt.Errorf("%s answered with an unreadable body: %w", what, err))
	}
	return nil
}

// MaxDetailBytes bounds the detail of a game's refusal that the lobby passes
// on to the player. A longer one is cut at a rune boundary.
const MaxDetailBytes = 1024

func boundedDetail(detail string) string {
	if !utf8.ValidString(detail) {
		detail = strings.ToValidUTF8(detail, "�")
	}
	if len(detail) <= MaxDetailBytes {
		return detail
	}
	cut := MaxDetailBytes
	for cut > 0 && !utf8.RuneStart(detail[cut]) {
		cut--
	}
	return detail[:cut]
}
