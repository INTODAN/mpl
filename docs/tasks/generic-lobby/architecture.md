# Architecture and explicit decisions

This is an implementation design, not a statement that these features already exist. [Contracts](contracts.md) define exact behavior; [tasks](README.md) define ownership.

## Current code and extraction

`backend/handlers/connect.go` mixes socket connection with game creation/join and returns user `0`. `backend/dal/game.go` always creates room name `test-game`; a missing room lookup returns `(nil, nil)`, which the connect handler currently accepts. `Player` in `backend/models/game.go` stores only game ID and team color. There is no authenticated identity, membership lifecycle, or connection table to reuse.

Move active room names, membership, team selection, identities, and leases into the new package. Keep `Game.ID`, generations, maps, castles, commands, soldier groups, simulation, and their numeric `TeamColor` fields in Island Battles. Retain a nullable unique `Game.LobbyID` link. A backend adapter maps stable generic team order to existing colors 1..N; color 0 remains neutral.

## Package graph

```text
lobby/                   independent module and shared assets
  domain/                types, repository ports, policy, and state rules
  application/           commands, binding, and expiry sweeping
  auth/                  guest bearer credentials, Centrifugo connection JWT signing
  postgres/              lobby-owned GORM models, schema, aggregate/identity/outbox stores
  httpapi/               reusable Huma DTOs, handlers, public route registration
  realtime/              Centrifugo protocol handlers and gocent adapter
  delivery/              retrying outbox/revocation worker
  internal/testutil/     disposable PostgreSQL fixture; no backend imports
backend/lobbyadapter/    Island Battles binding service construction and endpoint
backend/dal/lobby_binding.go  Game persistence and stable team-color mapping
lobby/cmd/export-lobby-openapi/  offline public lobby specification export
lobby/api/              checked-in OpenAPI and event schema artifacts
lobby/clients/lobby-python/   generated HTTP SDK plus separately maintained asyncio wrapper
lobby/integration/     actual Go + PostgreSQL + Centrifugo + Python acceptance suite
```

The domain package defines interfaces; adapters depend inward. Huma registration is reusable, with a thin call from existing `backend/routes/`. PostgreSQL and gocent are injected; importing the package must not open connections, migrate, start goroutines, read environment variables, or terminate the process.

## Identity and transport

Issue a random guest player UUID and a 256-bit opaque session bearer. Persist only the bearer hash; session lifetime is 24 hours. Each guest has exactly one session in v1; devices may share it. A client must retain the credential to retain identity across process restarts. Session expiry requires a new guest; account recovery is out of scope. HTTP authenticates this credential. A separate authenticated endpoint signs a 5-minute Centrifugo connection JWT with `sub=player_id` and an expiry no later than session expiry. A token callback obtains replacements. No client supplies a trusted player ID.

Centrifugo authenticates connections using that JWT. Disable the old connect proxy; connecting or reconnecting never creates/joins a lobby or game. Subscribe and subscription-refresh proxies authorize the authenticated Centrifugo user against durable membership. Protect proxies with a backend-only shared header. Do not issue subscription JWTs: they can bypass subscription proxy membership checks. Disable automatic personal subscriptions and single-connection restrictions; each client explicitly subscribes.

Channels are `lobby:<game_key>:<lobby_uuid>` and `player:<player_uuid>`. Canonical parsing rejects alternate encodings, extra segments, and reserved delimiter injection. The room namespace carries only version invalidations, never names, teams, join codes, tokens, or game payloads. The private namespace is reserved for owner-authorized invalidations; it is not a generic arbitrary-payload publication API.

Leaving immediately changes authorization for subsequent HTTP requests and future subscription authorizations. A read already authorized may finish. Existing subscriptions are unsubscribed through a durable revocation job; expiry also rechecks membership. Do not promise instantaneous transport revocation. A departed socket can briefly see only opaque invalidations. Disable client presence/history queries and join/leave broadcasts so these do not disclose the roster. Server-side history recovery is allowed for invalidations.

## Membership and application leases

A member occupies one seat regardless of socket count. Each running client chooses a new random `client_instance_id`; several clients can use the same guest credential. Create/join establishes a durable 45-second lease; a connected SDK client sends a heartbeat every 15 seconds. Pause heartbeat while the SDK is disconnected. This is application-reported liveness, not proof of a healthy socket and not a security boundary against a player retaining their own seat.

Each membership has a monotonically increasing generation, changed on leave/rejoin. Heartbeat/release carries both generation and client instance. Old activity cannot revive a new membership. A member remains reserved until 60 seconds after their latest effective lease expiry (release can shorten, never extend, an expiry). A sweeper runs every 5 seconds, but the deadline is computed from stored lease times, not from when the sweeper happens to run. Restart must not extend reservations. Commands also enforce due deadlines under the lobby lock.

During grace, retain team, ready state, and host. After grace, waiting-room members become inactive, clear team/ready, and transfer host deterministically. In started rooms, freeze the game roster; expiry marks the member disconnected and removes live access/host eligibility, but the start roster remains immutable. Rejoining a started room is allowed only for a player in that frozen roster and restores their original team. No new player can join. Explicit leave from a started room uses the same inactive-access state; it does not rewrite the game's start roster.

## Atomicity and reconciliation

Lock one lobby row before checking capacity, teams, host, phase, generations, and deadlines. Read the entire aggregate in a consistent transaction. Increment its version once for a visible committed change and insert an invalidation outbox row in that transaction. Heartbeat timestamp extensions alone do not increment the version. Store failures roll back both data and event. An outbox publisher retries after failures; duplicate delivery is expected.

Clients subscribe and wait for acknowledgement before reading the snapshot. SDK callbacks enqueue signals and return quickly. A single worker fetches the snapshot, comparing its version to the highest buffered event version; it repeats if the snapshot is behind. Full snapshots replace state, so duplicate/out-of-order invalidations never roll the view backward. Fetch a fresh snapshot after every successful (re)subscription, even if event recovery reports success; recovery and heartbeat versions are additional hints, not the only authority. Reconcile at least every 15 seconds while subscribed to handle a committed but delayed publication.

## Boundaries of this implementation

`POST /game/from-lobby` explicitly creates or returns Island Battles state for a started `island-battles` lobby. It snapshots the frozen team mapping and is idempotent under a unique lobby link. It does not generate a map or generation; existing game operations retain their current scope. Other registered game keys can use the lobby without importing or creating Island Battles state. Test with a second key, `quiz-demo`, whose only purpose is proving reuse.

The implementation uses one backend process initially. Durable rows, locks, and job claim leases must still withstand two worker instances in tests; this prevents retry races and permits restart. No Redis, Lambda, map subscriptions, or Pro-only Centrifugo feature is required.

## Verified references and implementation checks

- Huma exposes OpenAPI from registered operations; use the same registration for serving and offline export. [Huma OpenAPI](https://huma.rocks/features/openapi-generation/)
- Subscribe/refresh proxy envelopes, trusted static headers, and lack of disconnect/unsubscribe hooks are documented by Centrifugo. Verify the selected v6 image in the live tests. [Proxy protocol](https://centrifugal.dev/docs/server/proxy)
- History retention is bounded; reconnect can require a snapshot. [History/recovery](https://centrifugal.dev/docs/server/history_and_recovery), [presence](https://centrifugal.dev/docs/server/presence)
- Use the official asyncio SDK's connection/recovery machinery. Do not build another WebSocket reconnect loop. [Python SDK](https://github.com/centrifugal/centrifuge-python)
- Initial tool pins for validation are `openapi-python-client==0.29.0` and `centrifuge-python==0.6.0`, with Python 3.14. Commit a dependency lock and verify their compatibility rather than using a floating latest version. [Generator release](https://github.com/openapi-generators/openapi-python-client/tree/v0.29.0), [SDK release](https://github.com/centrifugal/centrifuge-python/tree/0.6.0)
