# Task 13 — Backend wiring, configuration, and transport

Status: implemented; see [verification evidence](implementation-status.md).

## Read first and prerequisites

- Read [README](README.md), [architecture](architecture.md), [contracts](contracts.md), and [test matrix](test-matrix.md).
- Requires 03 and 07–12; stage configuration work earlier, but do not claim integration before those tasks land.
- Read the backend-development skill. Keep generic package imports independent of `backend/`.

## Exclusive ownership

- Own `backend/main.go`, `backend/dependencies/`, `backend/routes/`, `backend/handlers/env.go`, and removal of `backend/handlers/connect.go`; also own `backend/internal/testutils/test_database.go` updates needed for the error-returning initializer.
- Own `centrifugo/centrifugo-config.toml`, `docker-compose.yml`, root local environment examples, and `clients/main.go` Go example.
- Own `go.mod` and `go.sum` centrally for every task. Other agents send dependency requests instead of editing them.
- Own adjacent bootstrap/config/route tests; no generic service, schema implementation, game model/DAL, or generated Python edits.
- Coordinate existing ping behavior with 09; keep its trusted backend publication working without reopening global player subscriptions.

## Backend assembly steps

1. Replace hard-coded dependency credentials/URLs with explicit startup configuration and validate required values before serving.
2. Define and document stable environment names for PostgreSQL URL, API listen address, WebSocket public URL, Centrifugo API URL/key, signing key, and proxy secret.
3. Register `island-battles` and `quiz-demo` game keys; configure contract timing defaults through validated policy, not scattered constants.
4. Open PostgreSQL with an error-returning constructor. Library dependency code must not call `log.Fatal` or terminate the process.
5. Initialize the nine-table generic lobby schema first, then AutoMigrate current game models and initialize island_game_team_bindings. Startup targets the current schema.
6. Keep this order explicit in one error-returning initialization function used by production and disposable backend fixtures.
7. Construct identity/authentication, JWT issuer, aggregate/job stores, service, realtime authorizer/transport, and Island Battles adapter explicitly.
8. Share one policy/clock configuration between service, authorization, sweeper, and workers.
9. Register public lobby Huma routes through 08's registrar and the game binding route through 12's adapter.
10. Register 09's internal proxy handlers separately; exclude them from public lobby-only OpenAPI export and credentials documentation.
11. Remove old `/connect` route/handler and its implicit create/join behavior; do not preserve a user-0 compatibility bypass.
12. Keep existing unrelated game routes functional and update changed Huma descriptions to their actual behavior.
13. Start sweeper and delivery loops only after successful initialization; propagate cancellation and startup errors to the entry point.
14. Use an explicit `http.Server`, signal-aware root context, bounded `Shutdown`, and owned worker cancellation/waiting.
15. Stop accepting requests, let active requests complete within a bound, stop background work, and close SQL connections last.
16. Workers must leave interrupted claims reclaimable; process exit is not delivery acknowledgement.
17. Update the Go example to use explicit HTTP guest/create/join/token flow and SDK reconnection.
18. Keep examples out of package initialization, accept configurable endpoints, and never embed server API/signing credentials in clients.

## Centrifugo and Compose steps

1. Select a real Centrifugo v6 patch tag or digest, record the version, and validate the exact checked-in TOML with that image.
2. Disable `[client.proxy.connect]`; remove its Compose override and the old URL.
3. Disable automatic user personal subscriptions and `single_connection`; all clients explicitly subscribe and multi-tab use is valid.
4. Configure `[channel.proxy.subscribe]` and `[channel.proxy.sub_refresh]` to 09's exact internal endpoints, with `http.static_headers` carrying the proxy secret.
5. Neither proxy object has a global `enabled`; enable `subscribe_proxy_enabled` and `sub_refresh_proxy_enabled` in protected namespaces.
6. Never forward the secret header through `http_headers` or `emulated_headers`. Clients must not replace the trusted static value.
7. Define canonical `lobby` and `player` namespaces and disable global unnamespaced subscribe permissions.
8. For `lobby`, set `history_size=128`, `history_ttl="300s"`, `force_recovery=true`, and `force_positioning=true` using verified keys.
9. Keep client publish, direct history/presence API access, join_leave, and force_push_join_leave disabled in protected namespaces.
10. Do not enable user-limited channels, anonymous authentication, map subscriptions, or Pro-only authorization shortcuts.
11. Use explicit local allowed origins, with documented deployment override; do not leave wildcard origins as the default.
12. Supply signing/API/admin keys through environment configuration; sample development values must be clearly local and must not appear in client artifacts.
13. Point Docker proxy endpoints at the actual backend host. Retain host-gateway mapping for host-run Go, or use service DNS for a containerized backend.
14. Ensure endpoint defaults work for the documented single chosen launch arrangement; do not mix container localhost with host localhost.
15. Provide required environment values consistently to backend and Centrifugo.
16. Validate Compose interpolation and image startup before claiming configuration done. Do not add Redis or change PostgreSQL deployment unnecessarily.
17. Document that refresh expiry scheduling may lag and does not control the 45s lease/60s application grace.

## Dependency ownership steps

- Collect exact dependency requests from 01–12/14 and keep the root module; no nested Go module is introduced.
- Prefer existing libraries/versions when adequate; justify additions, then run `go mod tidy` after their imports land.
- Preserve unrelated user dependency changes and report any dependency compatibility conflict before choosing incompatible versions.
- Do not put Python generator/runtime dependencies into Go module tooling; 15 owns their lock.

## Named tests and exact assertions

- `TestStartupRejectsMissingSecrets`: missing signing/API/proxy secret fails before HTTP serving or background work, without logging secret values.
- `TestStartupSchemaFailureDoesNotServe`: schema initialization error returns cleanly and closes initialized resources; no partial server/worker starts.
- `TestPublicLobbyRoutesAndOldConnectRemoval`: public routes exist, protected routes enforce bearer auth, and POST `/connect` has no create/join path.
- `TestInternalProxyRoutesRequireSecret`: internal paths reject unauthenticated requests and stay absent from public lobby spec.
- `TestGracefulShutdownStopsOwnedWorkers`: canceled root context shuts down HTTP/workers within bounds and closes DB only afterward.
- `TestGenericLobbySupportsSecondGameKey`: quiz-demo lobby creation never creates Island Battles game rows.
- `TestConfiguredCentrifugoProxyEndpoints`: running pinned image reaches both actual backend endpoints and accepts their protocol envelopes.
- `TestTwoSocketsOnePlayerRemainConnected`: same guest connects twice without one socket evicting the other.
- `TestCentrifugoNamespacePermissions`: valid member subscribes; stranger/direct publish/history/presence attempts fail; old unnamespaced access is denied.
- `TestPinnedHistoryRecoveryConfiguration`: temporary disconnect recovers retained invalidations; server restart still leads to client snapshot reconciliation.

## Verification

```sh
go test ./backend/... ./lobby/... -count=1
go vet ./backend/... ./lobby/...
go list -deps ./lobby/...
docker compose config --quiet
docker compose run --rm --no-deps centrifugo centrifugo checkconfig -c /centrifugo/centrifugo-config.toml
```

Use the pinned image's documented equivalent if its validation command differs. Run live checks with disposable infrastructure; final cross-language acceptance belongs to 18.

## Done and handoff

- Give 17/18 exact startup commands, environment example, addresses, tested image version, and shutdown/schema initialization ordering.
- Report test commands/results, dependency changes, retired routes, and unavailable live checks without treating them as passing.
- Confirm no generated/client artifact contains API keys, signing keys, or the proxy secret, and no generic package imports backend code.
