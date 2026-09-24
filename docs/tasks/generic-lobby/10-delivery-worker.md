# Task 10 — Durable invalidation and revocation delivery

Status: implemented; see [verification evidence](implementation-status.md).

## Read first and prerequisites

- Read [README](README.md), [architecture](architecture.md), [contracts](contracts.md), and [test matrix](test-matrix.md).
- Requires 04's claim/ack/retry store and 09's typed `RealtimeTransport`; 01 freezes both signatures.
- Use 02's disposable PostgreSQL fixture for claim-race tests. Apply backend-development conventions for Go work.
- Delivery is at least once. A database commit and external publication are never one atomic transaction.

## Exclusive ownership

- Own `lobby/delivery/` worker implementation, configuration validation, and adjacent tests.
- Own no schema/store queries, service mutation files, transport adapter, or application bootstrap.
- Request durable claim/query changes from 04, transport changes from 09, and module/bootstrap changes from 13.
- Do not add an in-memory queue as the authority for committed work or a second event schema.

## Required behavior

- Every committed public change already has its invalidation row written by 04 in the mutation transaction.
- Workers deliver persisted records, not fresh snapshots or newly synthesized event IDs.
- A claimed job remains recoverable after process exit, lease expiry, timeout, and lost acknowledgement.
- Claim/ack/retry must use a unique claim token, so an old worker cannot modify a newer worker's claim.
- Failed publication must never roll back, repeat, or recreate the original lobby mutation.
- Revocation targets one player's exact room channel across sockets; membership is the authority for future authorization.
- Newer active membership generations make old revocation jobs obsolete; obsolete work is acknowledged without unsubscribe.
- A rejoin racing after the final check may still get unsubscribed; the client reconciles/resubscribes, and membership remains intact.

## Implementation steps

1. Define a worker configuration using injected clock, cancellation context, transport, job store, and frozen authorization/state-read port.
2. Validate positive batch size, claim lifetime, network timeout, retry limits/durations, and bounded concurrency.
3. Choose a network timeout comfortably below claim lifetime. If processing serial batches, claim only work that can start before its lease expires.
4. Implement `RunOnce` or the frozen equivalent before the continuous loop so tests can drive deterministic passes.
5. Ask the store to atomically claim eligible due jobs and commit claims before performing network I/O.
6. Never retain a transaction or room lock while waiting for Centrifugo.
7. For invalidations, preserve original event_id/version/scope, validate the exact schema, construct the canonical channel, and publish once per attempt.
8. On success, acknowledge by job ID and claim token. Treat a lost/stale claim as already reassigned, not as permission to overwrite it.
9. If acknowledgement fails after a successful publish, retain recoverability; later duplicate publication is expected.
10. For revocations, read current membership generation/access state immediately before transport and skip a newer active generation.
11. Do not hold the lobby lock while unsubscribing to make that race disappear; the contract explicitly handles it at the client.
12. A failed revocation lookup must retry. Do not interpret a database outage as proof that access is absent or that a job is obsolete.
13. Publish/unsubscribe errors persist attempt count, next-attempt time, and a sanitized error summary through the claim token.
14. Use capped exponential backoff with bounded jitter; inject jitter or assert a range in tests instead of sleeping.
15. Retry transient failures indefinitely with a bounded delay; do not silently delete work after a maximum attempt count.
16. A corrupt/unsupported stored payload must remain inspectable and must not be published or repeatedly monopolize the worker.
17. Persist corrupt-payload failures through the existing capped retry/error fields, surface them clearly, and never claim such a row was delivered.
18. Continue independent jobs when one room/transport attempt fails; avoid starvation by respecting next-attempt timestamps.
19. The continuous loop waits using a timer/context, can be woken for shutdown, and does not busy-poll an empty queue.
20. Cancellation stops new claims; finish or cancel owned in-flight attempts according to the documented shutdown deadline.
21. A canceled attempt keeps a reclaimable row. Never acknowledge merely because the process is stopping.
22. Emit useful counts/errors with job ID, scope, type, and attempt; never log bearer tokens, proxy credentials, or API keys.
23. Two worker instances must be safe even though production initially runs one backend process.

## Named tests and exact assertions

- `TestCommittedChangeSurvivesPublishFailure`: lobby state/version persist, one pending job remains, and later retry publishes its original event.
- `TestRollbackHasNoDeliverableEvent`: failed mutation inserts no outbox job; the worker publishes nothing for that transaction.
- `TestPublishSuccessAckFailureDuplicatesSafely`: transport sees same event_id/version twice after retry, never two domain mutations.
- `TestExpiredClaimIsReclaimed`: crash simulation advances injected clock past claim expiry; a second worker obtains a new token and completes work.
- `TestStaleWorkerCannotAckOrRetryNewClaim`: old token affects zero current claims after reassignment and does not erase another worker's result.
- `TestTwoWorkersClaimIndependently`: PostgreSQL barriers prove a live job is claimed once at a time; all jobs eventually complete.
- `TestNetworkWorkHoldsNoDatabaseLock`: block fake transport, then mutate the same lobby successfully from another connection.
- `TestPublishUsesStoredInvalidationOnly`: exact six-field schema, canonical room channel, no roster lookup/embedding, and stable event_id.
- `TestOutOfOrderVersionsRemainUnmodified`: worker may deliver N+1 before N; payload versions stay unchanged and are not renumbered.
- `TestRevocationFailureRetries`: unsubscribe error persists retry metadata; a subsequent attempt succeeds without touching membership.
- `TestOldRevocationSkipsNewActiveGeneration`: leave generation 1, rejoin generation 2; old job is consumed without a transport call.
- `TestRevocationAfterRejoinRaceDoesNotMutateMembership`: force rejoin after lookup; unsubscribe may occur, but active generation 2 is unchanged.
- `TestRevocationLookupFailureDoesNotAcknowledge`: database read failure retains retryable work and sends no unsubscribe.
- `TestRetryBackoffIsCappedAndDurable`: repeated attempts increase delay within configured bounds and retain it after rebuilding the worker.
- `TestCorruptJobCannotLeakOrStarve`: invalid schema is not published; error is visible and unrelated valid jobs progress.
- `TestCancellationLeavesRecoverableClaims`: shutdown returns within its bound; unfinished job can later be reclaimed.
- `TestSlowBatchDoesNotStartExpiredClaims`: blocked first attempts cannot cause later jobs to execute with already expired ownership.

## Verification

```sh
go test ./lobby/delivery/... -count=1
go test -race ./lobby/delivery/... -count=1
```

Use fake transports for deterministic failure barriers and real disposable PostgreSQL for claims. No real-time sleeps for claim expiry/backoff assertions.

## Done and handoff

- Give 13 the constructor, validated settings, run-loop lifecycle, and shutdown sequence.
- Give 18 reproducible failure hooks for publish-down, crash-after-publish, and stale revocation acceptance scenarios.
- Record changed files, commands/results, delivery/ack race evidence, and any unavailable PostgreSQL checks.
- Confirm no exactly-once delivery promise, silent job dropping, or network call under a database lock remains.
