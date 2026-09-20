# Generic lobby domain

The root package owns domain records, policy, pure helpers, application services and repository ports. `auth`, `postgres`, `httpapi`, `realtime`, and `delivery` import inward. The root must never import those adapters, Huma/GORM/gocent, or `backend`. Importing this package performs no I/O. The only non-standard helper dependency is Unicode case folding from `golang.org/x/text/cases`.

Service construction and transaction helpers:

```go
func NewService(store Store, clock Clock, identifiers IdentifierSource, policy Policy) (*Service, error)

// Private service dependencies:
// store Store; clock Clock; identifiers IdentifierSource; policy Policy
func (service *Service) runMutation(ctx context.Context, scope Scope, command timedMutation) (Aggregate, error)
// timedMutation is func(*Aggregate, time.Time) (Change, error)
func TransactNormalized(ctx context.Context, store Store, clock Clock, policy Policy, scope Scope, command Mutation) (Aggregate, error)
```

The pure expiry normalizer is:

```go
func NormalizeExpired(aggregate *Aggregate, now time.Time, policy Policy) (Change, error)
```

`TransactNormalized` performs one `Store.Transact`. Inside its callback it captures one UTC clock value, normalizes the original aggregate, then runs the command against `aggregate.DeepCopy()`. A successful command replaces the aggregate and returns `MergeChanges(cleanup, commandChange)`. A rejected command discards its copy, returns cleanup with a nil callback error so cleanup commits, then returns the captured command error after commit. A store failure wins over that captured error. A normalization error rolls back. Callbacks must contain no network calls or external side effects. `runMutation` and exported `TransactNormalized` share a private `transactNormalizedTimed` helper. Service callbacks receive the same captured UTC time used for normalization, so no clock boundary can change halfway through a command; realtime authorization uses the exported helper.

For example, at an expired host's grace deadline, normalization deactivates that host, queues a generation-specific revocation and transfers host. Its attempted start is rejected after cleanup; the store nevertheless commits expiry and one new version/invalidation. No phase/start-roster edits from the rejected command survive. On successful start, any cleanup and the start share one version increment. Build every response from the post-commit returned aggregate.

Frozen pure helpers:

```go
DefaultPolicy(gameKeys ...string) Policy
(Policy).Validate() error
(Policy).ValidateGame(string) error
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

Policy defaults are explicit: `DefaultPolicy("island-battles", "quiz-demo")` supplies 15s heartbeat, 45s lease, 60s grace, 5s sweep, 24h session, 5m connection JWT and 60s subscription authorization. `Policy{}` is invalid. Policies must have positive durations, heartbeat shorter than lease, sweep no longer than lease or grace, token/subscription lifetimes no longer than session, and a nonempty unique list of canonical game keys. Constructors validate then retain a detached registered-key slice.

`Store.Create` atomically saves its original authorized `BuildSnapshot` response alongside the validated create fingerprint and request key; code uniqueness collisions return the internal `ErrJoinCodeCollision` sentinel, which services bound to five attempts. `Change.PrivateChanged` preserves lease/counter writes without implying an event. `Change.PublicChanged` implies persistence and one invalidation/version increment. Revocation intents contain scope/player/generation; the store allocates the durable event UUID. `MergeChanges` preserves first-seen order and deduplicates exact intents.

`IdentityStore.CreateGuest` inserts a `Player` and its one `Session` atomically. `LookupByBearerHash` and `LookupByPlayer` return stored `SessionVerification{Principal, ExpiresAt}`, nil on absence and errors on infrastructure failure. `CredentialHash` is a SHA-256 `[32]byte`. The verifier, not the lookup, rejects expired sessions. `Authenticator.Authenticate` takes the complete Authorization header; `IssueConnectionToken` takes its verified result. Neither input grants caller-chosen identity.

`SubscriptionAuthorizer.AuthorizeSubscription(ctx, authenticatedPlayerID, canonicalChannel)` returns `SubscriptionAuthorization.AllowedUntil`; use typed `ErrPermissionDenied`/`ErrUnauthenticated` for denials and `ErrUnavailable` for outages. Parsing enforces syntax; policy validates game registration separately. `RealtimeTransport.Unsubscribe` takes player ID then exact channel.

`DeliveryStore.Claim` accepts current time, bounded limit and positive claim duration; it increments attempts, assigns a fresh token/deadline and commits before returning. Every `DeliveryJob` has a stable ID, discriminator and exactly one typed payload with a stable EventID. `Ack(ctx, jobID, token, now)` and `Retry(ctx, RetryRequest)` condition on token and an unexpired claim; false indicates lost ownership. Retry sets sanitized failure/next-attempt without resetting attempts. `CurrentMembership(ctx, scope, playerID)` returns optional current generation/active state; an error never implies an absent or obsolete membership. Network adapters never receive a raw database handle.

`BuildSnapshot` filters inactive history, orders active members and teams, copies nullable fields and the frozen roster, emits non-null arrays and excludes leases/session information. Its caller must normalize under lock before building the response. The HTTP adapter separately enforces presence for required nullable `team_id` and required boolean `ready` before creating domain requests.

Production dependencies are `SystemClock{}` (UTC wall clock) and `CryptoIdentifiers{}` (cryptographic UUIDv4, unbiased join-code sampling, random byte slices). Their methods perform clock/random reads only when called; pure validation/copy helpers do not use them. `Bytes` rejects negative lengths without panicking.
