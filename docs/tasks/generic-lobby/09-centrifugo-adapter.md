# Task 09 — Centrifugo authorization proxies and transport adapter

Status: implemented; see [verification evidence](implementation-status.md).

## Read first and prerequisites

- Read [README](README.md), [architecture](architecture.md), [contracts](contracts.md), and [test matrix](test-matrix.md).
- Requires 01's frozen ports, 03's live identity/session lookup, and 04's store. Integration also requires 05 and 07 before completion.
- Read the backend-development skill; generic handler placement here is explicitly required by the task architecture.
- Use the official [proxy protocol](https://centrifugal.dev/docs/server/proxy) and [permission precedence](https://centrifugal.dev/docs/server/channel_permissions).
- Coordinate the exact tested Centrifugo v6 image with 13; do not assume a floating v6 image proves compatibility.

## Exclusive ownership

- Own `lobby/realtime/` implementation, proxy DTOs, registration helpers, gocent adapter, and adjacent tests.
- Own no domain ports, JWT issuance, public player routes, delivery worker, TOML/Compose, or bootstrap changes.
- Request module changes from 13; the repository already uses `github.com/centrifugal/gocent/v3 v3.4.0`.
- Reuse 01's channel helpers; do not create a differently permissive local parser.

## Authorization promises

- HTTP membership changes immediately govern subsequent authorization. Existing in-flight requests may finish.
- Existing subscriptions are revoked eventually; transient invalidations contain no roster, join code, team, or credential.
- Subscription expiry rechecks authorization but is not the application reconnect-grace clock.
- Never expose a method that publishes arbitrary caller-controlled payloads to room/player channels.
- No connect proxy, subscription JWT issuance, user-limited channel shortcut, Redis, or Pro-only capability is required.

## Implementation steps

1. Implement the frozen `SubscriptionAuthorizer` using live identity/session expiry and the aggregate store.
2. Room authorization parses `lobby:<game_key>:<lobby_uuid>`, verifies registered game scope, normalizes deadlines, and requires active membership.
3. Use 05's exported TransactNormalized helper (which invokes 07's NormalizeExpired), not the package-private Service.runMutation method, so denial does not roll back due cleanup.
4. Private authorization accepts only canonical `player:<authenticated_player_uuid>` and a live identity/session.
5. Never derive authority from request `data`, display name, join code, or an unverified user header.
6. Clamp expiry to the guest's unique session lifetime via 03's player-to-session lookup; devices share that session, with no caller-selected session metadata.
7. Register POST `/internal/lobby/centrifugo/subscribe` and `/internal/lobby/centrifugo/sub-refresh` through a reusable internal registrar.
8. Verify `X-Lobby-Proxy-Secret` before trusting body fields; compare secrets without logging values, reject an empty configured secret.
9. Bound body size, validate required `client`, `user`, and `channel` fields, and accept harmless unknown protocol fields.
10. Match lowercase protocol JSON keys exactly. Internal routes must not appear in the generated public lobby SDK.
11. Allow subscribe with HTTP 200 `{"result":{"expire_at":<unix expiry>}}`; expiry is normally now+60s, never zero.
12. Deny subscribe with HTTP 200 `{"error":{"code":403,"message":"permission denied"}}`.
13. Allow sub-refresh with the same result expiry; deny expired access with `{"result":{"expired":true}}`.
14. Missing/wrong proxy secret returns HTTP 401/403 before authorization; malformed request returns a controlled bad request.
15. Database/timeouts return transient failure, not a fabricated permanent membership denial. Fail closed and retain diagnostic context internally.
16. Implement typed `RealtimeTransport` operations for invalidation publication and user unsubscribe from one exact room channel.
17. Validate the invalidation envelope before JSON serialization; only schema_version, event_id, game_key, lobby_id, version, and type are allowed.
18. Use backend gocent APIs with context deadlines. Treat both transport errors and Centrifugo application errors as failure.
19. Unsubscribe by `user` and exact `channel`, omitting client/session filters so all that player's sockets in the room are covered.
20. Never globally disconnect a player to revoke one room. Do not hold a database transaction across either network operation.
21. Keep constructor arguments explicit: URL/key/proxy secret/clock/store are injected; no package-init environment reads or network calls.

## Configuration handoff to 13

- Configure `[channel.proxy.subscribe]` and `[channel.proxy.sub_refresh]` endpoints; these objects have no `enabled` field.
- Enable `subscribe_proxy_enabled` and `sub_refresh_proxy_enabled` on each protected namespace.
- Set the secret through each proxy's `http.static_headers`; never also forward that header through `http_headers` or `emulated_headers`.
- Disable automatic personal subscription and single-connection enforcement; clients subscribe explicitly and multiple tabs are supported.
- Disable anonymous/global unnamespaced subscribe permission, client publication, direct history/presence access, and join/leave broadcasts.
- Lobby history uses size 128 and TTL 300s; enable `force_recovery` and `force_positioning` using verified image keys.
- Do not use `$` private-prefix channels for proxy authorization; use the canonical names from the contract.
- The v6 refresh scheduler may lag by a minute; do not promise a strict 60-second transport cutoff.

## Named tests and exact assertions

- `TestProxySecretPrecedesIdentityTrust`: missing/wrong secret cannot call identity/store authorization despite a forged valid user body.
- `TestSubscribeWireEnvelopes`: golden JSON asserts exact lowercase result/error keys and positive Unix expiry, without Huma wrappers.
- `TestRefreshWireEnvelopes`: expired access uses result.expired=true; success clamps expiry; database failure stays transient.
- `TestCanonicalChannelIsolation`: wrong game, extra segments, uppercase UUID, injected delimiters, unknown namespace, and malformed UUID fail.
- `TestRoomSubscriptionRequiresCurrentMembership`: stranger/former member denied; grace member allowed; overdue member normalized then denied.
- `TestPrivateChannelRequiresExactOwner`: one valid user cannot authorize another player's channel; dead identity/session fails.
- `TestUntrustedSubscribeDataIgnored`: body metadata cannot change player identity, membership, channel scope, or expiry.
- `TestConnectJWTNeverCreatesMembership`: transport use cannot invoke create/join or write game rows.
- `TestInvalidationPayloadHasExactFields`: valid JSON has only the six contract fields and no snapshot or extensible payload.
- `TestPublishErrorsPropagate`: timeout, network failure, and HTTP-success protocol error remain retryable transport errors.
- `TestUnsubscribeTargetsOnlyRequestedRoom`: gocent request contains user/channel and no global disconnect or socket-specific filter.
- `TestAdapterCancellation`: canceled context exits publication/unsubscribe promptly and releases resources.
- `TestInternalRoutesAbsentFromPublicSpec`: public OpenAPI export contains neither internal path nor proxy credential schema.

## Verification

```sh
go test ./lobby/realtime/... -count=1
go test -race ./lobby/realtime/... -count=1
```

Use an HTTP fake for gocent wire/error cases, and disposable PostgreSQL for deadline authorization. Real proxy behavior is additionally required in 18.

## Done and handoff

- Give 10 the transport/error semantics and 13 the internal registrar plus exact proxy config examples.
- Give 16 the subscribe denial/unsubscribe behavior to implement terminal denial versus resubscribe reconciliation.
- Record the tested v6 version, configuration keys, wire fixtures, commands/results, and any unverified live check.
