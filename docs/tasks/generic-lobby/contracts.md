# Frozen implementation contract

All tasks use these decisions. Task 01 turns them into compilable Go types/interfaces and fills in mechanical signatures before parallel work begins. Changes after that gate require coordinated updates to this document, affected tasks, schemas, and tests.

## Domain and defaults

Use UTC timestamps, canonical lower-case UUID strings, and snake_case JSON names. Versions and membership generations are positive `int64` values (OpenAPI `integer/int64`); do not encode them as floating-point numbers. All lists serialize as `[]`, never `null`. Use explicit nullability: team_id, host_player_id, disconnected_until, and released_at may be null in their relevant DTOs/domain records. In a team-selection request, team_id is required but nullable; an omitted field is invalid. Timestamps such as disconnected_until are optional nullable fields where the response schema says so.

| Type | Fields and invariants |
| --- | --- |
| `Scope` | `GameKey string`, `LobbyID string`; game key matches `^[a-z][a-z0-9-]{0,31}$` and is registered in server configuration |
| `Principal` | `PlayerID`, `SessionID`, immutable `DisplayName`; produced only by credential verification, never body/header player IDs |
| `Lobby` | UUID, game key, display name (1–80 trimmed Unicode characters), cryptographically random 8-character uppercase join code, capacity (2–64), phase `waiting/started/closed`, version, nullable host ID, created time |
| `Team` | UUID, lobby ID, name (1–40 characters), capacity (1–64), immutable order (1..N); 2–16 teams, unique names after trim/case folding, sum capacities >= lobby capacity |
| `Member` | UUID, player ID, display name copied from verified Principal, generation, active flag, nullable team, ready flag, joined_at, join_sequence, nullable disconnected_until |
| `Lease` | lobby, player, generation, client instance UUID, expires_at, nullable released_at; one row per tuple; never a seat of its own |
| `StartRoster` | immutable list of player/team/member IDs and team order captured on successful start; separate from live access state |
| `Snapshot` | lobby metadata, ordered teams, active members, immutable start roster if started, caller's membership generation, room channel; no credential/session/lease secrets; members ordered by join_sequence then ID |
| `Invalidation` | `schema_version=1`, UUID `event_id`, game_key, lobby_id, version, `type="lobby.changed"`; no snapshot/body extension point |
| `Revocation` | player, scope, departed membership generation, event ID; worker skips if a newer active generation exists |

Join codes use a fixed alphabet excluding ambiguous characters, are unique within a game key, and are normalized by ASCII trim+uppercase before lookup. Keep the exact alphabet in one helper and test it. A closed lobby's code is not reused. Display names are labels and may repeat. Do not accept client-chosen lobby IDs, team IDs, host IDs, versions, membership generations on join, or join codes on create.

Persist a room-level `next_join_sequence` counter initialized after creator sequence 1; allocate and increment it under the room lock for each new or reactivated membership. Never derive future ordering from only currently active members.

No public lobby-list/search endpoint. Join requires `(game_key, join_code)`. Only active members can fetch a snapshot or subscribe; a former member can retry leave to receive 204 without gaining read access. Wrong game scope and unknown room are indistinguishable to outsiders.

## Service and adapter ports

Task 01 owns the exact declarations in `lobby/domain/types.go`, `ports.go`, `errors.go`, `policy.go`, and `requests.go`. Do not put GORM/Huma/gocent types into domain ports. Use `context.Context` as the first parameter for every I/O operation. A configured `Service` implements the following application port:

```go
type Application interface {
    Create(context.Context, Principal, CreateRequest) (Snapshot, error)
    Join(context.Context, Principal, JoinRequest) (Snapshot, error)
    Snapshot(context.Context, Principal, Scope) (Snapshot, error)
    SelectTeam(context.Context, Principal, Scope, SelectTeamRequest) (Snapshot, error)
    SetReady(context.Context, Principal, Scope, ReadyRequest) (Snapshot, error)
    Start(context.Context, Principal, Scope) (Snapshot, error)
    Leave(context.Context, Principal, Scope) error
    Heartbeat(context.Context, Principal, Scope, HeartbeatRequest) (HeartbeatResult, error)
    ReleaseLease(context.Context, Principal, Scope, ReleaseLeaseRequest) error
}
```

`CreateRequest`: request_id UUID, game_key, name, capacity, teams (name/capacity only), client_instance_id. `JoinRequest`: game_key, join_code, client_instance_id. `SelectTeamRequest`: nullable team_id. `ReadyRequest`: ready bool. Heartbeat/release contain client_instance_id and membership_generation. `HeartbeatResult` contains lobby version, membership_generation, lease_expires_at, and disconnected_until if applicable; this response does not include a full snapshot.

Define a `Clock.Now() time.Time`, injectable random ID/code/credential source, and `Policy` containing heartbeat/lease/grace intervals and the registered game keys. Production constructors reject invalid policies; tests can shorten durations without real sleeping.

The aggregate store owns transactions and persistence. Its minimum shape is:

```go
type Mutation func(*Aggregate) (Change, error)
type Store interface {
    Create(context.Context, Principal, CreateRequest, Aggregate) (Snapshot, error)
    ResolveCode(context.Context, string, string) (Scope, error)
    Read(context.Context, Scope) (Aggregate, error)
    Transact(context.Context, Scope, Mutation) (Aggregate, error)
}
```

`Aggregate` contains lobby, teams, member history, leases, and start roster, but no database handles. `Change` distinguishes public state changes, private lease-only writes, and revocation intents. Store methods return detached copies. `Transact` acquires the lobby row lock, loads children consistently, invokes the callback once (no automatic retry of arbitrary callback side effects), persists changed values including zero/NULL, and atomically writes invalidation/revocation rows. It increments version once if public state changed. Service callbacks never publish, call game code, or perform other network I/O. `Create` atomically writes creator membership/lease, teams, version 1, invalidation, and the idempotency record. Read-only `Read` uses one consistent transaction. Construct all successful Snapshot/Heartbeat responses from the returned post-commit aggregate, after the store increments version, never from a value captured before commit.

Define separate `IdentityStore` (hashed session lookup/create and player lookup), `Authenticator` (create guest/authenticate), `ConnectionTokenIssuer`, `SubscriptionAuthorizer`, and `ChangePublisher` (push a committed invalidation and unsubscribe revoked players from the exact channel, rechecking each revocation first) ports. Use small typed records and typed errors; task 01 must define their full signatures so later agents do not guess. Each guest creation atomically creates one new player and exactly one session; v1 cannot create another session for an existing player. Multiple devices share that bearer. Subscription authorization can therefore look up the single session by authenticated player UUID and clamp expiry to it, without trusting caller metadata. Enforce unique session player_id. Account/session expansion is a future contract change.

The root `Service` constructor and dependency fields are owned by 05. Tasks 06/07 implement additional methods in their own files against those dependencies. Task 01 publishes the exact planned constructor signature to prevent an editing conflict. All three service tasks use the same `Service.runMutation` helper owned by 05. It calls `NormalizeExpired(aggregate *Aggregate, now time.Time, policy Policy) (Change, error)`, implemented by 07; 01 defines that signature and examples before they begin. Task 01 also fixes deep-copy and Change-merge helpers so failed command validation cannot leak partial changes.

## State transitions

- Create: authenticated guest becomes host and first active member, unassigned and not ready. Create creates no Island Battles game. Capacity includes creator and all members in grace.
- Join waiting: reserve one seat and an unassigned member. An already active member rejoining is a successful no-op for roster/version and adds/renews only the supplied client lease. Returning inactive players increment membership generation and get a new join_sequence, unassigned/not ready.
- Select team: active members select only their own team in the same lobby, while waiting. Same team is a no-op. Switching or clearing team resets ready=false. A failed selection keeps the old team and readiness. Team capacity counts active members including grace.
- Ready: only waiting, active, assigned members can set true. Setting false is allowed while unassigned. Repeating the same value is a no-op.
- Start: current active host only; phase waiting; at least two active members; all assigned and ready; each has at least one lease with `expires_at > now` (grace alone is insufficient). Teams may be empty. Capture immutable roster, phase started, one version/event. Repeating start by the current host returns current snapshot without another event. Started blocks team and ready edits.
- Leave: deactivate caller, release their leases, clear live ready/team, create revocation intent, and deterministically elect the lowest active join_sequence (ID tie-break) as host. Last active member closes a waiting lobby. In a started lobby, preserve frozen start roster and allow roster members to return; do not close solely because everyone is temporarily inactive. Closed lobbies never reopen. Repeating a past member's leave returns success without another event. An outsider gets 404.
- Started rejoin: only frozen-roster identities can reactivate; increment generation, restore frozen team, keep ready=false, preserve frozen roster and membership ID. A returning active player only renews/adds a lease. If no active host exists, the first reactivated member becomes host. Do not re-run game creation or allow a new team choice. A non-roster identity presenting a started room code receives 404 not_found; do not expose its roster or offer an alternative conflict status.
- Host loss during grace does not transfer host. Expiry after grace behaves like leave, except that it is driven by durable deadlines. Started rosters remain immutable.

Before each lobby command, subscription authorization, and snapshot read, normalize due deadlines under the same lobby lock. `runMutation` wraps exported `TransactNormalized(ctx context.Context, store Store, clock Clock, policy Policy, scope Scope, command Mutation) (Aggregate, error)`, implemented by task 05 so task 09 can reuse normalization from a different package. `TransactNormalized` first normalizes the loaded aggregate, then runs the requested command on a deep copy. If the command fails, discard that copy, persist only normalization changes, and return the captured domain error after the transaction commits. If it succeeds, persist the copy and merge both Change values, producing at most one version increment. Database failure takes precedence over the captured domain error. This is one transaction, without a check/write gap; a rejected command must not roll back overdue cleanup forever. Store callbacks still execute exactly once. No external network call occurs while locked.

Time boundaries: leases are live strictly before expires_at; the effective expiry of each current-generation lease is `min(expires_at, released_at)` if released, otherwise expires_at. Grace is over at `now >= max(effective expiries)+60s`. Include released leases in that maximum, so an all-released member still has a defined deadline; release after natural expiry never extends grace. Create/join always creates at least one lease. Remaining live leases win. Heartbeat after lease expiry but before grace ends may renew; after grace it cannot revive membership, and the client must explicitly join. Releasing/heartbeating an old generation is 409, checked before looking up the tuple. Unknown current-generation heartbeat is 409 stale_membership; unknown current-generation release is 204 without mutation. A released tuple heartbeat or heartbeat for an inactive historical membership is also 409 stale_membership. Release of an already released current-generation tuple stays 204 even if the member is now inactive; an outsider with no membership history is 404. Release is idempotent for an already released tuple; heartbeat cannot reopen a released tuple. New processes use new client UUIDs. Expiry applied by one request and a concurrent heartbeat serialize on the same lobby lock.

## HTTP surface

Base prefix `/lobby/v1`. Route parameter names below are exact. Protected endpoints use `Authorization: Bearer <guest credential>`, documented by OpenAPI security scheme `LobbyBearer`. Proxy endpoints have separate server-only authentication and are excluded from the generated player SDK.

| Method + path | operationId | Input | Success |
| --- | --- | --- | --- |
| POST `/sessions` | `createLobbySession` | display_name (1–40 trimmed characters) | 201: player_id, access_token, expires_at |
| POST `/connection-token` | `createLobbyConnectionToken` | empty object | 200: token, expires_at, websocket_url |
| POST `/lobbies` | `createLobby` | CreateRequest | 201 Snapshot, including idempotent replay |
| POST `/memberships` | `joinLobby` | JoinRequest | 200 Snapshot |
| GET `/games/{game_key}/lobbies/{lobby_id}` | `getLobby` | scope path | 200 Snapshot |
| PUT `/games/{game_key}/lobbies/{lobby_id}/team` | `selectLobbyTeam` | team_id, explicitly required nullable | 200 Snapshot |
| PUT `/games/{game_key}/lobbies/{lobby_id}/ready` | `setLobbyReady` | ready, explicitly required boolean | 200 Snapshot |
| POST `/games/{game_key}/lobbies/{lobby_id}/start` | `startLobby` | empty object | 200 Snapshot |
| DELETE `/games/{game_key}/lobbies/{lobby_id}/membership` | `leaveLobby` | no body | 204 |
| POST `/games/{game_key}/lobbies/{lobby_id}/heartbeat` | `heartbeatLobby` | client_instance_id, membership_generation | 200 HeartbeatResult |
| DELETE `/games/{game_key}/lobbies/{lobby_id}/leases/{client_instance_id}` | `releaseLobbyLease` | generation required query parameter | 204 |

Use named, exported DTOs with `Body` fields and explicit JSON tags. Public route registration must also register a named `LobbyInvalidation` component for the event schema even though no HTTP operation returns it; offline export and generation must preserve it. Never expose GORM models. Descriptions must explain reconnect vs membership, idempotency, started-room rejoin, and auth. Required nullable `team_id` means omission is an error, whereas JSON null clears the team; use a presence-aware input representation and test it with Huma.

Errors use Huma problem JSON plus a stable application `code` extension: `unauthenticated` (401); `not_found` (404, including outsider/scope mismatch); `not_host` (403 for a member); `lobby_full`, `team_full`, `phase_conflict`, `not_ready`, `stale_membership`, `idempotency_conflict` (409); `invalid_input` (422, including invalid same-lobby team); `unavailable` (503 transient database/dependency failure); unexpected errors (500 with no SQL/secret disclosure). Framework body/path validation uses 422. Missing/invalid credentials must be 401 even if a player_id is supplied elsewhere or the body/path parameters are malformed. Enforce bearer authentication in middleware before Huma input validation; OpenAPI security metadata alone does not enforce it.

Create idempotency scope is `(player_id, request_id)`. Hash a canonical validated payload including client_instance_id. Identical retries return the original committed create response; changed payload is 409. The initial response may be stale, so the wrapper subsequently subscribes/fetches. Keep records indefinitely in v1. Random code collisions retry at most 5 times with a fresh candidate and transaction; exhaustion is a controlled 503. No-op commands emit no additional invalidation, except separately due deadline normalization.

## Island Battles binding contract (not in lobby-only SDK)

Task 12 owns `POST /game/from-lobby`, operationId `createIslandGameFromLobby`, body `{game_key, lobby_id}` and authenticated LobbyBearer access. Only a current active host of a started `island-battles` lobby can call it. Both creation and replay return 200 `{game_id, lobby_id, teams:[{team_id, team_order, team_color}]}`. Missing auth is 401, unknown/wrong-game scope or outsider is 404, active nonhost is 403 not_host, waiting/closed is 409 phase_conflict. Reauthorize replays. Authorization occurs against a normalized snapshot; once authorized, the in-flight operation may finish using that immutable start state even if the host subsequently leaves. This is an explicit operation-boundary policy, not a cross-service atomic authorization claim.

`backend/models/lobby_binding.go` defines `island_game_team_bindings` with game_id, team_id UUID, team_order and team_color; enforce unique (game_id, team_id) and (game_id, team_color). `backend/dependencies/database.go` migrates it alongside the other game models after lobby schema creation. Persist all configured teams with color=immutable order (1..N), keeping 0 neutral. No generic package imports this mapping. The adapter creates game and mapping together or neither.

## PostgreSQL schema rules

Explicit names: `lobby_players`, `lobby_sessions`, `lobby_rooms`, `lobby_teams`, `lobby_memberships`, `lobby_leases`, `lobby_start_roster`, and `lobby_create_requests`. No table holds messages waiting to be sent. UUID primary keys except stable composite identity where appropriate. Do not embed GORM soft deletion in active membership tables; persist active/generation state explicitly.

Constraints include unique `(game_key, join_code)`, unique `(lobby_id, player_id)`, unique `(lobby_id, team order)`, unique team-name normalization within lobby, unique lease tuple, and unique create request tuple. Enforce team-in-lobby relationships with composite foreign keys; preserve member history after leave. Foreign keys do not replace the lock required for capacity checks. Use CHECK constraints for local bounds and positive versions/generations. Expiry columns require indexes.

Queries use model filters and GORM clauses; optional reads use `Limit(1).Find(&slice)`. Schema initialization uses fixed definitions and no interpolated user values. Lock ordering is always room → memberships/teams/leases. Network I/O happens only after commit.

## Realtime wire protocol

Private POST endpoints: `/internal/lobby/centrifugo/subscribe` and `/internal/lobby/centrifugo/sub-refresh`, with `X-Lobby-Proxy-Secret`. Validate it before accepting `user` or `channel`. Match the selected Centrifugo v6 request schema and exact lowercase JSON keys. Unknown fields can be ignored, required fields cannot.

Subscribe allow: HTTP 200 `{"result":{"expire_at":<unix now+60>}}`; deny: HTTP 200 `{"error":{"code":403,"message":"permission denied"}}`. Refresh allow uses result expire_at; denied/expired membership uses `{"result":{"expired":true}}`. Infrastructure failures must fail closed without being disguised as permanent membership removal. Missing proxy secret is HTTP 401/403, not a successful protocol envelope. Clamp expiry to the applicable identity/session lifetime.

Room authorization requires parsed game/lobby match and active membership after deadline normalization. Player authorization requires exact UUID match to authenticated user and live identity/session. Check the chosen version's refresh scheduling; do not equate subscription expiry timing with application lease timing.

Pushes publish only the invalidation schema, to the canonical room channel, and retry briefly within the request. Delivery can duplicate or arrive out of order; version is state order and event_id is delivery identity. Revocations unsubscribe a player on the exact room channel across their sockets. Before each attempt, recheck the current membership and skip a player who has rejoined; a race after the check can still unsubscribe a newly rejoined socket, which must trigger authorization and snapshot reconciliation rather than destroy membership.

Pin a verified Centrifugo v6 patch/digest during task 13. Enable history size 128, history TTL 5 minutes, recovery and positioning for the lobby namespace using that version's actual configuration keys. Deny client publish, direct history, and presence access. Leave join/leave events off. Disable global unnamespaced subscribe permission. Set explicit local allowed origins, environment-provided keys/URLs, and Docker host proxy overrides. Do not add Redis or Pro-only hooks.

## Python contract

`lobby/clients/lobby-python/generated/` is a complete generated installable package with import `lobby_api_client`. Handwritten `lobby_client/` imports it and owns credentials, an HTTP client, the official Centrifugo client, subscriptions, a bounded signal queue, one reconciliation worker, and heartbeat scheduling. Python remains >=3.14, matching the repo. Generator and runtime dependencies are locked separately or in one clearly grouped lock.

Public wrapper operations: create_session, create_lobby, join_lobby, select_team, set_ready, start, snapshot, leave, close, and `updates(scope)` as an async iterator of accepted Snapshots. Surface terminal authentication/membership/schema errors to this iterator and any pending initial-ready wait; transient failures retry without silently terminating monitoring. `close()` releases this client's leases when possible and closes its transports; it does not implicitly leave durable memberships or cancel unrelated tasks. `leave()` leaves for the identity across all of its clients. Use async context management and retain credentials explicitly; never create a guest or lobby from a reconnect callback.

On every subscription acknowledgement, queue a reconcile signal. While awaiting the initial snapshot, buffer the highest event version; a snapshot is accepted only if it cannot overwrite a newer accepted snapshot. If behind buffered version, request another. Bound memory by coalescing highest versions, not dropping the fact that reconciliation is needed. Recovered=false, epoch changes, duplicates, publication before HTTP command response, server restart, and dropped live delivery all converge through snapshots. Unknown schema_version triggers a surfaced compatibility error, never applying an unknown event.

Use generated async command calls, including credential/token methods. Centrifugo supplies reconnect/backoff and token callback machinery. Publication/subscription callbacks do not await SDK operations or slow HTTP. Only the worker performs reconciliation. A server unsubscribe can require an explicit client resubscribe; retry with backoff while membership is authorized, stop on confirmed 404/401, and support explicit join again. Retry transient HTTP reads with bounded backoff. Do not automatically retry mutations unless idempotence is guaranteed, retaining the same create request_id. Missing bearer credentials after restart require a new explicit create_session.
