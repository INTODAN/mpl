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
(Policy).ValidateGameSettings(context.Context, string, json.RawMessage) (json.RawMessage, error)
NormalizeGameSettings(json.RawMessage) (json.RawMessage, error)
(Policy).ValidateScope(Scope) error
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

`FindMember` and `FindLease` return mutable pointers into the aggregate; missing records return nil. Lease helpers take player ID then generation; `FindLease` additionally takes client UUID. Lease lists belong to one aggregate. `LatestEffectiveLeaseExpiry` includes released leases of the requested generation and returns false for absent history. Released effective expiry is min(expiry, release), so a late release cannot extend grace. `HasLiveLease` excludes released tuples and uses strict `expiry > now`. `ElectHost` uses active members ordered by join sequence then membership ID, returning a detached player-ID pointer or nil.

The exact lease error ordering is membership-history existence (outsider: `ErrNotFound`), generation equality (otherwise `ErrStaleMembership`), then tuple policy. Unknown current-generation heartbeat and inactive/released-tuple heartbeat return `ErrStaleMembership`. Unknown current-generation release and repeated released-tuple release succeed without changes, including an inactive historical membership. Heartbeats never create or reopen tuples. Explicit join with a new client UUID establishes a lease. Persist `Lobby.NextJoinSequence=2` after the creator receives sequence 1; increment under the room lock for every new/reactivated membership, never derive it from active members.

Policy defaults are explicit: `DefaultPolicy(registrations...)` supplies 15s heartbeat, 45s lease, 60s grace, 24h session, 5m connection JWT and 60s subscription authorization. `Policy{}` is invalid. Policies must have positive durations, heartbeat shorter than lease, token/subscription lifetimes no longer than session, and a nonempty list of registrations with unique canonical game keys. Constructors validate then retain a detached registration slice.

The host declares each game with a `GameRegistration`. The lobby applies its fields without knowing the game, and a zero field keeps the default behavior, so new declarations are added as new fields:

- `Key` is the canonical game key.
- `ValidateSettings` checks the optional `game_settings` object of a create request. A nil validator means the game takes no settings and accepts only `{}`.

`ValidateGameSettings` first canonicalizes the settings with `NormalizeGameSettings`: one compact JSON object with sorted keys and at most `MaxGameSettingsBytes`, and `{}` when the request had none. The validator receives that canonical object. A rejection becomes `invalid_game_settings` with the validator's message as the client-facing detail, and the create request fails without writes. A validator that can't run returns the context's error or one wrapping `ErrUnavailable`. The lobby stores the canonical object with the room, includes it in the create fingerprint and returns it as `lobby.game_settings` in every snapshot, including the started snapshot a game binding receives. Settings never change after creation.

`Store.Create` atomically saves its original authorized `BuildSnapshot` response alongside the validated create fingerprint and request key; code uniqueness collisions return the internal `ErrJoinCodeCollision` sentinel, which services bound to five attempts. `Change.PrivateChanged` preserves lease/counter writes without implying an event. `Change.PublicChanged` implies persistence and one invalidation/version increment. Revocation intents contain scope/player/generation. The store validates them before commit and never calls the network. `MergeChanges` preserves first-seen order and deduplicates exact intents.

`IdentityStore.CreateGuest` inserts a `Player` and its one `Session` atomically. `LookupByBearerHash` and `LookupByPlayer` return stored `SessionVerification{Principal, ExpiresAt}`, nil on absence and errors on infrastructure failure. `CredentialHash` is a SHA-256 `[32]byte`. The verifier, not the lookup, rejects expired sessions. `Authenticator.Authenticate` takes the complete Authorization header; `IssueConnectionToken` takes its verified result. Neither input grants caller-chosen identity.

`SubscriptionAuthorizer.AuthorizeSubscription(ctx, authenticatedPlayerID, canonicalChannel)` returns `SubscriptionAuthorization.AllowedUntil`; use typed `ErrPermissionDenied`/`ErrUnauthenticated` for denials and `ErrUnavailable` for outages. Parsing enforces syntax; policy validates game registration separately. `ChangePublisher.PublishChange(ctx, lobby, change, stillDue)` pushes a committed change: one `lobby.changed` invalidation carrying the room's version when `PublicChanged`, and an unsubscribe from the room channel for each revocation, which covers every device of that player. Before every attempt it asks the `RevocationCheck` whether each revocation still applies.

`BuildSnapshot` filters inactive history, orders active members and teams, copies nullable fields and the frozen roster, emits non-null arrays and excludes leases/session information. Its caller must normalize under lock before building the response. The HTTP adapter separately enforces presence for required nullable `team_id` and required boolean `ready` before creating domain requests.

Production dependencies are `SystemClock{}` (UTC wall clock) and `CryptoIdentifiers{}` (cryptographic UUIDv4, unbiased join-code sampling, random byte slices). Their methods perform clock/random reads only when called; pure validation/copy helpers do not use them. `Bytes` rejects negative lengths without panicking.

## Pushing changes to clients

Postgres holds only state. No table holds messages waiting to be sent, and nothing runs between requests:

- `application.NewPublishingStore(store, publisher)` wraps a `domain.Store`. After `Create` or `Transact` commits, it pushes the change before returning, so the request that made a change also delivers it. Before each unsubscribe attempt it re-reads the room and skips a player who holds a newer active membership, so a push that waits between retries never drops the subscription of a player who rejoined meanwhile.
- `realtime.NewPublisher(client, logger, realtime.DefaultPushRetry())` is the shared helper that pushes to Centrifugo's HTTP API. It sends all of one call's messages in a single request, makes up to three attempts within about a second, and retries only the messages that failed, with stable idempotency keys. What still fails is logged as an error and returned. The commit stands, and clients recover by re-reading over HTTP. `PublishChange` serves the lobby, and `Publish(ctx, ...Publication)` serves a game's own events, such as Island Battles' generation events on `PlayerChannel`.
- Expiry is applied lazily. Every command, the heartbeat included, and every Centrifugo subscribe or sub-refresh proxy call normalizes due lease and grace deadlines in its transaction and pushes the result. A lobby with a connected member is therefore normalized within one heartbeat or refresh, and a lobby nobody is connected to is normalized on its next access.

Centrifugo keeps history for both the `lobby` and `player` namespaces, so a reconnecting client recovers recent messages before it reconciles over HTTP.

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
