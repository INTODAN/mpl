# 03 — Guest identity and connection-token authentication

Prerequisites: 01 and 02. Read the identity, proxy, and HTTP contracts; current connect handler demonstrates the placeholder behavior to replace, not an authentication pattern to preserve.

## Exclusive ownership

Create `lobby/auth/service.go`, `credentials.go`, `connection_token.go`, and tests; implement `lobby/postgres/identity.go` and its tests against task 02 models. Task 08 owns HTTP routes, task 09 proxy handlers, and task 13 secret/config wiring and dependency changes. Do not edit those files.

## Steps

1. Implement guest creation: validate trimmed display name, generate unrelated player/session UUIDs and at least 32 random bearer bytes, encode bearer URL-safely, hash it, and atomically insert player plus its unique session. Expiry is injected-clock now+24h. Return raw bearer only in the successful creation response; never persist/log it.
2. Each create_session call creates a new player. Exactly one session belongs to each player in v1; multiple clients may share that bearer. There is no caller-selected player ID, account lookup by name, or renewal into an existing identity.
3. Authenticate HTTP credentials by strict Bearer parsing and hash lookup. Use a constant-time comparison where comparing secret material directly. Reject absent, empty, malformed, unknown, expired, or otherwise invalid credentials as typed unauthenticated. `now == expires_at` is expired.
4. Authentication must never infer identity from display_name, room code, path, user-supplied player ID, or the old constant `0`. Respect request cancellation and map infrastructure failures separately from bad credentials.
5. Implement IdentityStore optional reads using model/clause filters. Populate Principal.DisplayName from stored immutable guest identity, never request labels. Lookup-by-player for proxy authorization returns the single session expiry and whether it is currently valid. Never turn `(nil, nil)` into an authenticated empty principal.
6. Implement connection JWT issuance after bearer verification: stable sub=player UUID, exp=min(now+5m, session expiry), iat from injected clock, and the selected supported HMAC algorithm/secret. Use a maintained JWT library or carefully bounded standard signing implementation agreed with coordinator; task 13 owns go.mod/go.sum updates. Fail startup/config validation for missing secret, do not silently use committed development keys.
7. Return a configured public websocket_url; never return the internal Centrifugo HTTP API URL/key or proxy secret. Bind the response to the verified principal. A refreshed connection token uses the same player; it creates no guest, lobby, member, lease, or game.
8. Expose constructors with injected repository, clock, randomness and signing config. No globals, env reads, log.Fatal, or HTTP registration in this package. Document configuration errors for bootstrap owner.

## Required tests

| Test | Arrange and assert |
| --- | --- |
| `TestGuestCreationStoresOnlyCredentialHash` | Create a guest, authenticate returned bearer, query database; raw bearer is absent and player/session IDs are distinct |
| `TestGuestsWithSameDisplayNameRemainDistinct` | Create twice with same name; different identities and credentials, no ownership collision |
| `TestGuestCreationIsAtomic` | Force session insertion/randomness failure; no orphan player or reusable credential |
| `TestGuestHasExactlyOneSession` | Direct duplicate player-session insertion fails the DB constraint; clients sharing existing bearer still authenticate |
| `TestAuthenticateRejectsInvalidCredentials` | Table-test missing/empty/malformed/unknown and expired bearers, including exact expiry boundary |
| `TestAuthenticateIgnoresClaimedIdentity` | Valid bearer for A plus input claiming B never produces B's principal |
| `TestConnectionTokenClaimsAndExpiry` | Verify signature using configured secret, sub equals actor, exp is capped by session expiry and no secrets appear in claims |
| `TestConnectionTokenRefreshHasNoLobbySideEffects` | Issue multiple tokens and compare room/member/lease/game counts and player identity |
| `TestConnectionTokenFailsClosed` | Empty signing config, crypto failure, cancelled context and expired guest produce errors, never usable JWTs |
| `TestLookupByPlayerHonorsSessionExpiry` | Proxy identity lookup gives same lifetime as bearer authentication, including exact expiry |

HTTP header spoofing and live Centrifugo wrong-signature tests are additionally owned by 08/18. Do not claim JWT compatibility from merely base64-decoding claims; verify cryptographically here and through Centrifugo in 18.

## Verification and handoff

Run `go test ./lobby/auth ./lobby/postgres -run 'Guest|Authenticate|ConnectionToken|LookupByPlayer'` with the disposable PostgreSQL fixture. Run focused vet/format checks. Hand task 08 exact response/auth port usage, task 09 the session-expiry lookup, and task 13 required env/config fields. Record expected 401 vs dependency-error behavior and do not print test credentials in output.
