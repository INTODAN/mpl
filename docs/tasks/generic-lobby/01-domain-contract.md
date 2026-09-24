# 01 — Compile the domain contract before parallel implementation

Prerequisites: none. Read [contracts](contracts.md), [architecture](architecture.md), the task index, and `backend/models/game.go`. Deliver Go declarations and pure domain helpers, not a functioning lobby service.

## Exclusive ownership

Create `lobby/domain/types.go`, `requests.go`, `ports.go`, `errors.go`, `policy.go`, `channels.go`, `copy.go` and their pure unit tests. Create `lobby/README.md` documenting package import direction. Do not edit backend files, module dependencies, service method implementation files, or other task documents. Request any module dependency promotion from task 13.

## Steps

1. Define every domain record and request in the contract, using named types and explicit enum constants. Separate mutable member access from immutable `StartRoster`. Membership identity survives rejoin; generation and join_sequence change.
2. Define `Application` with the exact methods in the contract. Define `Service`'s planned constructor contract as `NewService(store Store, clock Clock, identifiers IdentifierSource, policy Policy) (*Service, error)`; task 05 owns the actual struct/constructor. Authentication stays outside that constructor: callers supply verified `Principal`.
3. Fully declare Store/Mutation/Change/Aggregate. Specify which fields Change contains, including `PublicChanged`, lease/private persistence indication, and typed revocation intents. Define deterministic `MergeChanges` and deep-copy behavior. Never hide persistence behind `map[string]any`.
4. Fully declare `IdentityStore`, `Authenticator`, `ConnectionTokenIssuer`, `SubscriptionAuthorizer`, `DeliveryStore`, and `RealtimeTransport` before handing off. Include verified DisplayName and expiry in session verification results; signature must support lookup by bearer hash and by player UUID for the one-session policy. Subscription authorization returns an allowed-until timestamp or typed denial/transient error.
5. DeliveryStore declarations must support bounded batches, claim token/deadline, attempt count, next-attempt time, conditional ack/retry, and current member generation lookup. Jobs have a discriminator (invalidation/revocation), typed payload, and stable event ID. Network adapters never receive a raw GORM DB.
6. Fix `IdentifierSource` methods for UUID, join code, and cryptographic bytes, returning errors. Fix `Clock.Now`, Policy defaults (15/45/60/5 seconds, 24-hour sessions, 5-minute connection JWT), and registered-game validation. Distinguish zero-value configuration from defaults explicitly.
7. Declare the `NormalizeExpired(*Aggregate, time.Time, Policy) (Change, error)` contract in package documentation without a duplicate implementation/stub; task 07 implements it. Freeze exported `TransactNormalized(ctx context.Context, store Store, clock Clock, policy Policy, scope Scope, command Mutation) (Aggregate, error)` for task 05; its Service.runMutation is a thin wrapper and task 09 calls the exported helper. Specify the algorithm: normalize original; command on deep copy; on domain rejection persist normalization only and return that error after commit; storage error wins. Include an example showing an expired host's denied start still commits expiry.
8. Implement pure helpers for canonical game keys/UUID channels, code normalization, team/name validation, host election, effective lease expiry, deep copies, and changes merging. Include a persisted next_join_sequence counter and exact unknown/released lease error rules from the contract. Freeze names used by tasks 05–09 in `lobby/README.md`. Helpers must not run I/O or depend on adapters.
9. Define stable typed errors and code constants matching the HTTP table. Keep mapping to Huma in task 08. Avoid panics for validation failures, malformed channels, missing optional records, or empty member lists.
10. Coordinator checks that every dependent task can implement against these declarations. Record any clarification directly in shared contracts through the coordinator; do not let separate agents fill missing ports independently.

## Required tests

| Test | Arrange and assert |
| --- | --- |
| `TestChannelCanonicalRoundTrip` | Parse/build each valid game/lobby and player channel; exact equality and same scope |
| `TestChannelRejectsAmbiguousIdentifiers` | Reject extra colon, `#`, `$`, slash, percent-encoding, malformed/uppercase UUID, empty/unknown namespace and wrong game syntax |
| `TestJoinCodeNormalization` | Lowercase/ASCII surrounding whitespace normalize; wrong length/alphabet and Unicode lookalikes fail |
| `TestPolicyValidation` | Reject grace/lease/heartbeat combinations that cannot meet contract, empty registration, duplicate game keys and invalid durations |
| `TestTeamDefinitionValidation` | Reject duplicate case-folded names, too few/many teams, invalid capacity/sum; valid Unicode labels are preserved |
| `TestHostElectionDeterministic` | Equal join sequence resolves by ID; inactive members excluded; no eligible member gives null |
| `TestEffectiveLeaseExpiry` | Live/released/all-released cases and release after natural expiry produce the documented maximum without extending it |
| `TestAggregateDeepCopyIsolation` | Mutate nested copied members, nullable fields, leases, teams and start roster; original remains byte-for-byte equivalent |
| `TestMergeChanges` | Cleanup plus command makes one public-change flag, preserves every revocation once, and preserves private writes without inventing a public change |

## Verification and handoff

Run `gofmt` on owned Go files, `go test ./lobby`, and `go vet ./lobby`. The domain build must work without Docker/network or importing `backend/`, Huma, GORM, or gocent. Record constructor/port names and examples for each next owner. No placeholder implementations returning successful empty values. This task is the mandatory gate before task 02 and parallel service work.
