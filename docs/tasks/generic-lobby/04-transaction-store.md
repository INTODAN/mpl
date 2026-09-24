# Task 04 — Locked aggregate store and atomic delivery records

Status: implemented; see [verification evidence](implementation-status.md). Prerequisites: tasks 01 and 02 complete; exact ports and records are frozen.
Read [contracts](contracts.md), [architecture](architecture.md), and [schema task](02-schema-fixtures.md).
Read `backend/dal/game.go` for struct/clause filtering conventions, not its unsafe max-plus-one player assignment.
Apply `.agents/skills/backend-development/SKILL.md`; inspect task 01's types, errors, and policy helpers.

## Exclusive ownership

- `lobby/postgres/store.go`, `store_create.go`, `store_read.go`, `store_mutation.go`, and matching `store*_test.go`.
- `lobby/postgres/outbox_store.go` and `outbox_store_test.go`.
- Do not edit shared `models.go`/`schema.go`; request changes from task 02.
- Do not implement identity auth, lifecycle policy, publication workers, or backend schema wiring in this task.

## Deliverable

Implement frozen `Store` and `DeliveryStore` with real PostgreSQL transactions.
Every returned aggregate/snapshot is detached; callbacks receive no database handle.
Guarantee one room lock, consistent children, explicit zero/NULL writes, and atomic event insertion.
Lease changes alone create no public version/event; visible changes create exactly one invalidation.

## Aggregate operations

1. Resolve code by normalized game key/code using model filters, returning typed not-found on an empty optional lookup.
2. Include both lobby UUID and game key in scope lookups; a UUID alone does not establish authorization or scope.
3. `Read` uses a consistent transaction, such as repeatable read, so room and children share one database snapshot.
4. Order teams by team order, members by join_sequence then ID, and roster deterministically before returning.
5. `Transact` begins a transaction and selects the scoped room `FOR UPDATE` before reading any mutable children.
6. Load complete membership history, teams, leases, and start roster within that transaction.
7. Invoke the callback exactly once for this call. Do not retry callbacks after deadlocks or serialization failures.
8. Callback errors roll back all writes, version changes, outbox inserts, and revocations from that transaction.
9. Persist changed false/zero/NULL fields explicitly; GORM struct updates that omit zero values are insufficient.
10. Persist the room next_join_sequence counter atomically. Preserve membership IDs, old lease generations, joined history, and immutable start roster according to the callback's change.
11. For a visible change, increment version exactly once and generate one stable event ID before outbox insertion.
12. Serialize only frozen invalidation fields; snapshots, credentials, leases, teams, names, and join codes never enter events.
13. Insert each requested revocation in the same transaction, carrying scope/player/departed generation/event ID.
14. Lease-only changes persist without an invalidation; a true no-op writes no duplicate event.
15. Return the committed version and detached aggregate after successful commit; service response builders must use this returned aggregate; never report a pre-increment result.
16. Store methods do not publish or unsubscribe; network work belongs to tasks 09/10 after committed claim acquisition.

## Atomic creation and retry semantics

17. Use unique `(player_id, request_id)` to serialize duplicate creates; do not rely on a check followed by an unguarded insert.
18. Compare task 01's canonical validated payload hash, including client_instance_id; mismatch returns idempotency conflict.
19. Identical retry returns the original stored create response even if room membership/version has subsequently changed.
20. A successful first transaction inserts room, ordered teams, creator member/lease, version 1 event, and idempotency record together.
21. Generated code collisions must leave no partial room or request row; identify the specific code unique constraint.
22. Coordinate the single retry owner with task 05: at most five fresh-code attempts total, each in a fresh transaction.
23. Do not retry unrelated unique/FK/check failures as code collisions; exhaustion maps to controlled unavailability.
24. Keep create request records indefinitely for v1; no background expiry deletion in this task.

## Deadline and delivery integration

25. `DueScopes(now, limit)` finds expired candidates using indexed persisted lease/release times, including members with no live lease.
26. It is a candidate query only; sweeper/domain normalization rechecks effective expiry and grace under the room lock.
27. Released leases use min(expires_at, released_at) as effective expiry; the most recent effective lease for the current generation wins.
28. Ignore older membership generations for present deadlines; do not extend deadlines based on poll/worker restart time.
29. Support task 05's `runMutation`: normalize the loaded aggregate, run command on a deep copy, discard that copy on denial.
30. The service captures a command denial outside the callback and returns a successful Change containing normalization only; store commits it normally.
31. A callback error still rolls back everything; no store retries. Task 05 returns captured command errors after commit, with database errors taking precedence.
32. Claim ready delivery jobs in a short transaction with bounded batch size and row locking/skip-locked semantics.
33. Assign a fresh claim token and durable claim deadline; commit before returning jobs to a worker.
34. Ack/retry must compare job identity and claim token; an expired worker cannot acknowledge or reschedule another claim.
35. Retries preserve event ID/payload, increment attempt state, and persist next-attempt time.
36. An expired claim becomes eligible again; acknowledged jobs cannot be reclaimed for normal delivery.
37. Follow room → member/team/lease → outbox lock ordering; claim operations must not hold outbox locks while acquiring rooms.
38. Context cancellation and database errors return typed errors without leaking SQL or credentials through API-facing strings.

## Required PostgreSQL tests

- `TestStoreReadsConsistentSnapshot`: pause between parent/child loading while another connection commits; read returns one coherent version.
- `TestStoreScopeMismatchIsNotFound`: correct UUID with another game key returns not-found without child data.
- `TestStoreSerializesConcurrentMutations`: two barrier-synchronized transactions update one room; second observes first's committed change.
- `TestStoreInvokesCallbackOnce`: induced callback error yields one invocation and no automatic re-execution.
- `TestStoreRollsBackAggregateAndOutbox`: update multiple fields then fail before commit; persisted aggregate/version/jobs are unchanged.
- `TestStorePersistsFalseZeroNull`: clearing ready/team/host/deadline persists explicitly rather than retaining old values.
- `TestStoreLeaseOnlyMutationDoesNotInvalidate`: expiry extends, room version/event count remain unchanged.
- `TestStorePublicMutationWritesOneInvalidation`: multiple public field changes yield one version increment and one schema-valid event.
- `TestStoreMultipleRevocationsAreAtomic`: one mutation deactivating two members persists two revocations plus one invalidation.
- `TestCreateRequestConcurrentReplay`: two independent connections with same actor/request/payload return identical snapshot and one room/event.
- `TestCreateRequestPayloadConflict`: same key with changed client UUID or team input yields conflict and no extra room.
- `TestCreateReplayUsesOriginalResponse`: mutate room after create, then retry; replay returns the stored original version/body.
- `TestCreateCodeCollisionRollsBack`: collide on room code; no orphan team/member/request remains and a fresh attempt can succeed.
- `TestDueScopesUsesCurrentGenerationEffectiveExpiry`: old/released leases cannot conceal or extend the current member's true deadline.
- `TestDeliveryClaimsAreExclusive`: two workers claim concurrently; their active job ID sets are disjoint.
- `TestExpiredClaimCanBeReclaimed`: advance fake clock past claim deadline; same event ID gets a different claim token.
- `TestStaleClaimCannotAckOrRetry`: old token attempts affect zero rows after reclaim; new owner still controls job.
- `TestAcknowledgedJobIsNotReclaimed`: repeated scans after acknowledgement return no copy of the completed job.

Use task 02's PostgreSQL fixture, independent connections, barriers, and an injected clock; no race-proof sleeps.

## Verification and handoff

```sh
go test ./lobby/postgres -run 'Test(Store|Create|Due|Delivery|Expired|Stale|Acknowledged)' -count=1
go test -race ./lobby/postgres -count=1
go vet ./lobby/postgres
```

Handoff record names, constraint-to-error mapping, creation collision ownership, claim semantics, and exact commands/results.
Done requires database-backed rollback/concurrency tests, unchanged frozen ports, and agreement with tasks 05/07/10 on normalization and jobs.
