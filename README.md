# Generic lobby component

This directory is a self-contained component of the Island Battles monorepo. It owns the Go library, API contracts, client SDKs, examples, transport configuration, documentation, and shared test tooling.

```text
lobby/
  go.mod, go.sum       Independent Go module
  domain/             Types, ports, errors, policies, and pure state rules
  application/        Membership commands, teams, leases, binding, and publishing
  auth/               Guest credentials and connection tokens
  postgres/           Persistence and versioned schema migrations
  httpapi/            HTTP routes and DTOs
  adminmcp/           Read-only MCP tools for operators' questions about lobbies
  frontend/           Temporary browser playground and embedded asset handler
  realtime/           Centrifugo authorization and the push publisher
  internal/           Shared implementation helpers and test fixtures
  api/                OpenAPI and event schema artifacts
  cmd/                Offline OpenAPI exporter
  clients/            Go example and Python SDK, wrapper, CLI, and generator
  centrifugo/         Shared transport configuration
  integration/        Live tests and configurable host runner
  scripts/            Shared acceptance entry point
  docs/               Architecture and implementation records
```

All commands in this README run from this directory:

```sh
GOWORK=off go test ./...
GOWORK=off go vet ./...
go run ./cmd/export-lobby-openapi --output api/openapi.json --events-output api/events.json --check
uv run --project clients/lobby-python --locked pytest clients/lobby-python/tests
```

Go database tests use disposable PostgreSQL containers. On rootless Podman, run `systemctl --user enable --now podman.socket` once, then export `DOCKER_HOST=unix://$XDG_RUNTIME_DIR/podman/podman.sock` and `TESTCONTAINERS_RYUK_DISABLED=true` before running them. Live tests additionally need a host application implementing the lobby endpoints; see [the acceptance runner](integration/README.md). Game-specific endpoint checks belong to the host application's suite.

The module path remains `islandBattles/lobby`. Go consumers import `islandBattles/lobby/domain` for contracts and `islandBattles/lobby/application` for services; there is no root Go package or compatibility alias layer. It has no dependency or filesystem replacement pointing to the parent repository. To extract it, copy this directory, verify with `GOWORK=off`, then choose the published repository's module path and update imports. The monorepo's `go.work` and root `go.mod` local replacement are consumer-side wiring only. A plain `go test ./...` at the monorepo root tests the host module; use `go test ./... ./lobby/...` there to test both modules.

## Schema migrations

The PostgreSQL schema is versioned SQL in `postgres/migrations/`. A host calls `postgres.Migrate(ctx, db)` before using the stores. It runs every pending migration in one transaction under a PostgreSQL advisory lock and records each in `lobby_schema_migrations`, so concurrent runs wait for each other and a failed run changes nothing. A database whose lobby tables were created before versioning is recorded at migration 1 without running it, but only if it has every table and column that migration 1 creates. If some tables or columns are missing, `Migrate` fails and names them.

To change the schema, add the next file, `NNNN_description.sql` numbered without gaps, and update the GORM record in the same change; a test fails when records and columns differ. Never edit an applied migration: `Migrate` fails when an applied file has changed. Migrations cannot use statements that PostgreSQL refuses inside a transaction, such as `CREATE INDEX CONCURRENTLY`.

A host can version its own tables with `postgres.MigrationSet`, which takes its own history table and files. Island Battles does this for its map templates.

## Temporary browser frontend

The [frontend](frontend/README.md) is a plain HTML/CSS/JavaScript playground for two players. The host application lists its games when it mounts the frontend, so the frontend names none of its own. The Island Battles backend serves it at `/lobby/ui/`. Open that address in two tabs or browsers, choose a game, and create or join using the shared code. Assets are embedded in the Go executable; no frontend build or dependency installation is needed.

## Domain and application services

`domain/` owns records, request types, errors, policies, state helpers, and repository ports. `application/` owns commands and coordinates those contracts: `membership.go` groups create/join/snapshot, while teams, lifecycle, leases, binding, and publishing have focused files. Tests live beside the package they exercise.

Application services depend on domain contracts. Auth, PostgreSQL, HTTP, and realtime adapters depend inward on the domain and, where needed, application services. Neither inner package imports an adapter, Huma/GORM/gocent, or the game backend. Shared nullable-field helpers stay private under `internal/optional`. Importing a package performs no I/O.

Service construction and transaction helpers live in `application`:

```go
service, err := application.NewService(store, clock, identifiers, policy)
aggregate, err := application.TransactNormalized(ctx, store, clock, policy, scope, command)
```

The constructor accepts `domain.Store`, `domain.Clock`, `domain.IdentifierSource`, and `domain.Policy`. Transaction callbacks use `domain.Mutation`. The service keeps its dependencies and timed mutation helper private.

The pure expiry normalizer lives in `domain`:

```go
func NormalizeExpired(aggregate *Aggregate, now time.Time, policy Policy) (Change, error)
```

`TransactNormalized` performs one `Store.Transact`. Inside its callback it captures one UTC clock value, normalizes the original aggregate, then runs the command against `aggregate.DeepCopy()`. A successful command replaces the aggregate and returns `MergeChanges(cleanup, commandChange)`. A rejected command discards its copy, returns cleanup with a nil callback error so cleanup commits, then returns the captured command error after commit. A store failure wins over that captured error. A normalization error rolls back. Callbacks must contain no network calls or external side effects. `runMutation` and exported `TransactNormalized` share a private `transactNormalizedTimed` helper. Service callbacks receive the same captured UTC time used for normalization, so no clock boundary can change halfway through a command; realtime authorization uses the exported helper.

For example, at an expired host's grace deadline, normalization deactivates that host, records a generation-specific revocation and transfers host. Its attempted start is rejected after cleanup; the store nevertheless commits expiry and one new version/invalidation. No phase/start-roster edits from the rejected command survive. On successful start, any cleanup and the start share one version increment. Build every response from the post-commit returned aggregate.

Frozen pure helpers:

```go
DefaultPolicy(games ...GameRegistration) Policy
(Policy).Validate() error
(Policy).Game(string) (GameRegistration, error)
(Policy).ValidateGame(string) error
(Policy).ValidateGameSettings(context.Context, string, json.RawMessage, []TeamDefinition) (json.RawMessage, error)
NormalizeGameSettings(json.RawMessage) (json.RawMessage, error)
(Policy).ValidateSeatSettings(context.Context, string, json.RawMessage) (json.RawMessage, string, error)
NormalizeSeatSettings(json.RawMessage) (json.RawMessage, error)
(Member).IsSeat() bool
(Policy).ValidateScope(Scope) error
DefaultRetention() RetentionPolicy
(Policy).LobbyRetention(string) LobbyRetention
(Policy).RetentionCutoffs(time.Time) RetentionCutoffs
(RetentionResult).Backlog(int) bool
(*Lobby).MarkConnected(time.Time)
ValidateGameKey(string) error
ValidateUUID(string) error
NormalizeName(string, int) (string, error)
TeamNameKey(string) string
ValidateTeamDefinitions([]TeamDefinition, int) ([]TeamDefinition, error)
NormalizeJoinCode(string) (string, error)
RoomChannel(Scope) (string, error)
PlayerChannel(string) (string, error)
ParseChannel(string) (Channel, error)
ElectHost([]Member) *string
EffectiveLeaseExpiry(Lease) time.Time
LatestEffectiveLeaseExpiry([]Lease, string, int64) (time.Time, bool)
HasLiveLease([]Lease, string, int64, time.Time) bool
FindMember(*Aggregate, string) *Member
FindLease(*Aggregate, string, int64, string) *Lease
(Aggregate).DeepCopy() Aggregate
MergeChanges(...Change) Change
BuildSnapshot(Aggregate, string) (Snapshot, error)
```

`FindMember` and `FindLease` return mutable pointers into the aggregate; missing records return nil. Lease helpers take player ID then generation; `FindLease` additionally takes client UUID. Lease lists belong to one aggregate. `LatestEffectiveLeaseExpiry` includes released leases of the requested generation and returns false for absent history. Released effective expiry is min(expiry, release), so a late release cannot extend grace. `HasLiveLease` excludes released tuples and uses strict `expiry > now`. `ElectHost` uses active members that aren't [seats](#seats-that-no-person-plays), ordered by join sequence then membership ID, returning a detached player-ID pointer or nil.

The exact lease error ordering is membership-history existence (outsider: `ErrNotFound`), generation equality (otherwise `ErrStaleMembership`), then tuple policy. Unknown current-generation heartbeat and inactive/released-tuple heartbeat return `ErrStaleMembership`. Unknown current-generation release and repeated released-tuple release succeed without changes, including an inactive historical membership. Heartbeats never create or reopen tuples. Explicit join with a new client UUID establishes a lease. Persist `Lobby.NextJoinSequence=2` after the creator receives sequence 1; increment under the room lock for every new/reactivated membership, never derive it from active members.

Policy defaults are explicit: `DefaultPolicy(registrations...)` supplies 15s heartbeat, 45s lease, 60s grace, 24h session, 5m connection JWT, 60s subscription authorization and `DefaultRetention()`. `Policy{}` is invalid. Policies must have positive durations, heartbeat shorter than lease, token/subscription lifetimes no longer than session, a valid retention policy (see [Deleting old data](#deleting-old-data)), and a nonempty list of registrations with unique canonical game keys. Constructors validate then retain a detached registration slice.

The host declares each game with a `GameRegistration`. The lobby applies its fields without knowing the game, and a zero field keeps the default behavior, so new declarations are added as new fields:

- `Key` is the canonical game key.
- `ValidateSettings` checks the optional `game_settings` object of a create request against the request's validated teams, so a game can require settings that fit the lobby, such as a map with one starting position per team. A nil validator means the game takes no settings and accepts only `{}`.
- `StartRules` adds start conditions to the default ones: at least two members, at least one of them a person, each with a team, ready and connected, or `not_ready`. With `EveryTeamNeedsMember`, a start with an empty team fails with `empty_teams`, whose `team_ids` list the empty teams in team order. Snapshots carry the game's rules as `start_rules`, so clients can explain why the lobby can't start yet.
- `Retention` overrides the policy's `LobbyRetention`, the idle period and the maximum age after which the game's lobbies are deleted. Each zero period keeps the default.
- `ReadyGate` lets the game keep members from getting ready, and a lobby from starting, until they meet a condition of the game's own, such as a choice made in the game's API. The lobby calls it with the lobby's ID before the transaction of a ready request with `ready: true` or a start, so the game reads its own data without the room's lock held, and runs the `ReadyCheck` it returns inside the transaction: for the member getting ready (one `ReadyMember`, `atStart` false), after the lobby's own checks (active member, waiting phase, a team), and once for the whole start roster (every member's ID and display name, `atStart` true), after the start rules, so the game can name the members who must act in a refusal that the host sees. The check returns nil or `GameRequirement(problemType, message)`, which fails the request with 409 `game_requirement`, whose `type` is the game's problem type URI and whose `detail` is the game's message for the player. A gate or check that can't decide reports `unavailable`. Because the data is read before the lock, a member must keep meeting the condition once they do while the lobby waits: a game may let a choice change, but not disappear. Clearing readiness never asks the gate. A nil gate adds no condition.
- `ValidateSeat` lets the host add [seats that no person plays](#seats-that-no-person-plays), such as the game's bot, and checks each seat's `seat_settings`. It receives the settings as a canonical object and returns the seat's display name, such as `Bot (level 2)`. A rejection becomes `invalid_seat_settings` with the validator's message; a validator that can't run returns the context's error or one wrapping `ErrUnavailable`. A nil validator means the game has no seats, and every seat is refused with `invalid_seat_settings`.

`ValidateGameSettings` first canonicalizes the settings with `NormalizeGameSettings`: one compact JSON object with sorted keys and at most `MaxGameSettingsBytes`, and `{}` when the request had none. The validator receives that canonical object and the request's teams in order, after they pass `ValidateTeamDefinitions`. A rejection becomes `invalid_game_settings` with the validator's message as the client-facing detail, and the create request fails without writes. A validator that can't run returns the context's error or one wrapping `ErrUnavailable`. The lobby stores the canonical object with the room, includes it in the create fingerprint and returns it as `lobby.game_settings` in every snapshot, including the started snapshot a game binding receives. Settings never change after creation.

`Store.Create` atomically saves its original authorized `BuildSnapshot` response alongside the validated create fingerprint and request key; code uniqueness collisions return the internal `ErrJoinCodeCollision` sentinel, which services bound to five attempts. `Change.PrivateChanged` preserves lease/counter writes without implying an event. `Change.PublicChanged` implies persistence and one invalidation/version increment. Revocation intents contain scope/player/generation. The store validates them before commit and never calls the network. `MergeChanges` preserves first-seen order and deduplicates exact intents.

`IdentityStore.CreateGuest` inserts a `Player` and its one `Session` atomically. `LookupByBearerHash` and `LookupByPlayer` return stored `SessionVerification{Principal, ExpiresAt}`, nil on absence and errors on infrastructure failure. `CredentialHash` is a SHA-256 `[32]byte`. The verifier, not the lookup, rejects expired sessions. `Authenticator.Authenticate` takes the complete Authorization header; `IssueConnectionToken` takes its verified result. Neither input grants caller-chosen identity.

`SubscriptionAuthorizer.AuthorizeSubscription(ctx, authenticatedPlayerID, canonicalChannel)` returns `SubscriptionAuthorization.AllowedUntil`; use typed `ErrPermissionDenied`/`ErrUnauthenticated` for denials and `ErrUnavailable` for outages. Parsing enforces syntax; policy validates game registration separately. `ChangePublisher.PublishChange(ctx, lobby, change, stillDue)` pushes a committed change: one `lobby.changed` invalidation carrying the room's version when `PublicChanged`, and an unsubscribe from the room channel for each revocation, which covers every device of that player. Before every attempt it asks the `RevocationCheck` whether each revocation still applies.

`BuildSnapshot` filters inactive history, orders active members and teams, copies nullable fields and the frozen roster, emits non-null arrays and excludes leases/session information. Its caller must normalize under lock before building the response. The HTTP adapter separately enforces presence for required nullable `team_id` and required boolean `ready` before creating domain requests.

### Seats that no person plays

The host can fill a team with a seat that the game plays, such as a default bot, and play alone. `POST /lobby/v1/games/{game_key}/lobbies/{lobby_id}/seats` with `{team_id, seat_settings}` adds one, and `DELETE …/seats/{seat_id}` removes it; both return the snapshot and publish `lobby.changed`. `seat_settings` is the game's own JSON object (Island Battles: `{"bot_level": 2}`), canonicalized like game settings with `NormalizeSeatSettings` (at most `MaxSeatSettingsBytes`) and checked by the game's `ValidateSeat`, before the transaction, which also names the seat.

- **Who and when.** Only the host (otherwise `not_host`), only while the lobby waits (otherwise `phase_conflict`). A `team_id` that isn't one of the lobby's teams, and a `seat_id` that names no seat of the lobby, are `invalid_input`. Removing a removed seat again changes nothing. Adding is not idempotent: a retry after a lost response adds a second seat, which the host removes.
- **What a seat is.** A seat is a membership of kind `seat` with its `seat_settings`, so it has a stable member ID (the `seat_id`) and its own player ID, which a `lobby_players` row without a session holds. It counts toward the lobby's and the team's capacity (`lobby_full`, `team_full`) and toward the two members a start needs, is always ready on its team, holds no lease, never disconnects and never hosts. Expiry normalization skips it. Two seats of one name are told apart with a number, such as `Bot (level 2) #2`.
- **Snapshots.** Every member has `kind`, `human` or `seat`, and `seat_settings`, the seat's object or null for a person. Clients show a seat's `display_name` and leave its readiness alone.
- **Starting.** The start roster includes each seat with its player ID and team, like any member, and the started snapshot carries its settings, so the game binding plays it. A start still needs at least one person: the host, who starts it, must be on a team and ready. A lobby whose last person leaves has no host and closes, so seats alone never start. A `ReadyGate` checks only the roster's people; seats never get ready through it.
- **Storage.** Migration 0005 adds `kind` (`human` for existing rows) and `seat_settings` to `lobby_memberships`. A removed seat stays as inactive history, like a member who left. Retention deletes seats with their lobby, then their players once nothing references them.

Seats are memberships rather than a table of their own so that everything that names a lobby's players keeps working: the start roster's player and member keys, a game's tables with a foreign key to `lobby_memberships` (Island Battles' bot choices), and game bindings that read the roster's player IDs. Watchers of a started game would be a separate table, since they take no seat.

Production dependencies are `SystemClock{}` (UTC wall clock) and `CryptoIdentifiers{}` (cryptographic UUIDv4, unbiased join-code sampling, random byte slices). Their methods perform clock/random reads only when called; pure validation/copy helpers do not use them. `Bytes` rejects negative lengths without panicking.

## Pushing changes to clients

Postgres holds only state. No table holds messages waiting to be sent, and nothing runs between requests:

- `application.NewPublishingStore(store, publisher)` wraps a `domain.Store`. After `Create` or `Transact` commits, it pushes the change before returning, so the request that made a change also delivers it. Before each unsubscribe attempt it re-reads the room and skips a player who holds a newer active membership, so a push that waits between retries never drops the subscription of a player who rejoined meanwhile.
- `realtime.NewPublisher(client, logger, realtime.DefaultPushRetry())` is the shared helper that pushes to Centrifugo's HTTP API. It sends all of one call's messages in a single request, makes up to three attempts within about a second, and retries only the messages that failed, with stable idempotency keys. What still fails is logged as an error and returned. The commit stands, and clients recover by re-reading over HTTP. `PublishChange` serves the lobby, and `Publish(ctx, ...Publication)` serves a game's own events, such as Island Battles' generation events on `PlayerChannel`.
- Expiry is applied lazily. Every command, the heartbeat included, and every Centrifugo subscribe or sub-refresh proxy call normalizes due lease and grace deadlines in its transaction and pushes the result. A lobby with a connected member is therefore normalized within one heartbeat or refresh, and a lobby nobody is connected to is normalized on its next access.

Centrifugo keeps history for both the `lobby` and `player` namespaces, so a reconnecting client recovers recent messages before it reconciles over HTTP.

## Deleting old data

Lobby data is deleted once it is past its retention period, in small batches at the end of lobby requests. There is no timer, so when nobody uses the lobby nothing runs, and nothing grows either. `Policy.Retention` holds the periods, and `DefaultRetention()` gives:

| Data | Deleted | Default |
|---|---|---|
| Create requests | After the idempotency window, or with the lobby they created. A retry inside the window replays the original response; a later one, or one after its lobby was deleted, creates a new lobby. | 24 hours after the request |
| Sessions | After they have been expired for a grace period, which covers requests that verified one just before it expired | 1 hour after expiry |
| Players, seats' players included | Once no session, room host, membership, start roster or create request references them. Only players older than a session and its grace are checked, since younger ones still have theirs. | As soon as nothing references them |
| Lobbies, in any phase, with their teams, memberships, leases and start roster | When nobody has been connected for the idle period, and in any case at the maximum age. A `GameRegistration.Retention` can override either period for its game. | 1 hour idle, 1 day old |

Each room records `last_connected_at`, the last time a client created, joined, heartbeated, released a lease or left. Reading a snapshot doesn't count. The idle period and the maximum age must both be longer than a lease plus its grace, so a member who can still reconnect never loses the lobby to idleness. Started lobbies are deleted like the others: a game authorizes from its own frozen roster, takes no part in cleanup and keeps working after its lobby is gone.

- `postgres.NewRetentionStore(db)` implements `domain.RetentionStore`. `DeleteExpired(ctx, policy.RetentionCutoffs(now))` deletes create requests, then sessions, then lobbies, then the players they freed. Each step is its own transaction that locks at most `BatchSize` rows with `FOR UPDATE SKIP LOCKED` and deletes them, child rows before rooms. The steps are independent: one that fails is reported in the returned error, and the later steps still run. A lobby that a request holds is left for a later pass, so a pass never waits on a lobby in use, and passes on different instances don't wait on each other. It touches only lobby tables.
- `application.NewRetention(store, clock, policy, logger)` decides when a pass runs. `RunIfDue(ctx)` runs one when `PassInterval` has passed since the last pass in this process, or right away when the last pass filled a batch, so a backlog drains a batch per request. Only one pass runs at a time per process, and other requests skip instead of waiting. A pass outlives a canceled request, has its own 5 second timeout, and logs failures instead of failing the request.
- `httpapi.Register(api, app, authenticator, issuer, retention)` calls `RunIfDue` after each lobby request. Pass nil to delete nothing.

The defaults are a batch of 50 rows of each kind and a pass interval of one minute. A pass that finds nothing costs four indexed queries, about once a minute per instance, and a full pass is four short transactions that each lock at most 50 rows, typically a few milliseconds. Retention periods are hours long, so a minute's delay doesn't matter. A backlog, such as the first pass on a database that never deleted anything, drains at 50 rows of each kind per lobby request without making any one request much slower.

## Binding a game to a started lobby

Game integrations reuse three entry points:

- `application.NewGameBinding(gameKey, snapshotReader, persist)` checks scope, current membership, host, started phase, and frozen roster/team consistency on every request, including replays. The callback receives all configured teams in order and returns the game's own result type. Use the lobby application as the snapshot reader so membership expiry is normalized before authorization.
- `httpapi.RegisterGameBinding(api, operation, binding, authenticator)` registers a game-owned POST endpoint accepting `{game_key, lobby_id}`. It supplies bearer authentication, principal extraction, typed success responses, and shared problem responses. The operation supplies its path, ID, tags, and description. Game endpoints are registered explicitly and remain outside the lobby-only SDK export.
- `postgres.BindOnce(database, uniqueLobbyConstraint, load, create)` supplies the transaction and concurrent-conflict recovery for PostgreSQL-backed games. `load` returns nil when absent and an error for incomplete stored game state. `create` writes the game and its associated state using the supplied transaction. Only a conflict on the specified unique lobby-link constraint triggers a reload; all other errors roll back and propagate. Pass a database with the request context attached.

A game's constructor only selects its key and connects its persistence callback:

```go
binding, err := application.NewGameBinding("quiz-demo", lobbyService,
    func(ctx context.Context, started domain.Snapshot) (QuizMatch, error) {
        return quizStore.Bind(ctx, started)
    })
```

`quizStore.Bind` owns the game's schema, result type, and translation of lobby teams into game state. It must atomically create or return the same game for the lobby; PostgreSQL integrations can use `BindOnce` for that orchestration. It must not start external side effects inside the transaction. No game rows are created by the normal lobby start operation.

Island Battles connects these helpers through `backend/lobbyadapter`, with game-specific persistence in `backend/dal/lobby_binding.go` and model migration in `backend/dependencies/database.go`. The adapter uses the actual model types directly, without re-exporting aliases. It translates team order to numeric simulation colors and validates that persisted mappings match on replay. Generic lobby code does not import the backend or assume numeric game IDs or team colors.

## Admin MCP tools

`adminmcp` answers an operator's questions about lobbies and players through [Model Context Protocol](https://modelcontextprotocol.io) tools, on a server built with the official Go SDK. A game's own admin server registers them next to its game tools; Island Battles does so in [`backend/cmd/admin-mcp`](../backend/README.md#admin-mcp-server).

- `adminmcp.OpenDatabase(ctx, url, logger)` connects with a single connection whose sessions start with `default_transaction_read_only=on` and a 5-second `statement_timeout`. Statements always use the extended protocol, so one query is one statement. A superuser's sessions `SET ROLE pg_read_all_data`, with a warning, because a read-only transaction leaves a superuser powers such as reading server files. That is best-effort; only a role that is not a superuser makes the tools fully read-only.
- `adminmcp.ReadOnly(ctx, db, read)` runs `read` in a read-only transaction that is always rolled back, so a `SET` in it ends with it, and then releases the session's advisory locks, which outlive the rollback. Every tool runs in one, the game's included.
- `adminmcp.CheckCredentialsHidden(transaction)` fails with `ErrCredentialsReadable` when a role that is neither a superuser nor the table's owner can read `lobby_sessions.bearer_hash`, such as a read-only role whose grants a migration has outrun. Run it before any SQL an operator writes.
- `adminmcp.AddTools(server, db, options)` registers `overview`, `list_lobbies`, `get_lobby` and `find_players`. They read only the `lobby_*` tables, through the records in `postgres/models.go`, and never the sessions' `bearer_hash`. `Options.Games` adds the game's counts to `overview` and the linked game's ID to each lobby.

A member counts as connected while a lease of its current membership has neither expired nor been released, as `domain.NormalizeExpired` decides. The tools read this from the timestamps but never apply expiry, so they change nothing. `get_lobby` says when retention deletes the lobby at the latest, from `Options.LobbyRetention`, which defaults to `DefaultRetention()`.
