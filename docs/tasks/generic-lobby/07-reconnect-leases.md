# Task 07 — Durable client leases and reconnect grace

Status: implemented; see [verification evidence](implementation-status.md).

## Read first and prerequisites

- Read [README](README.md), [architecture](architecture.md), [contracts](contracts.md), and [test matrix](test-matrix.md).
- Requires 01's frozen helper/service signatures and 04's transactional store; integration requires 05 and 06.
- Apply the backend-development skill for Go/GORM conventions. All persistence goes through the store port.
- Confirm 01 defines the shared `NormalizeExpired` signature and 05 defines the single-transaction `runMutation` helper before coding.
- Do not substitute socket presence, Centrifugo join/leave events, or an in-memory timer for durable deadlines.

## Exclusive ownership

- Own `lobby/application/leases.go`, `lobby/application/sweeper.go`, and their adjacent `_test.go` files.
- Implement the frozen `Application.Heartbeat` and `Application.ReleaseLease` methods on 05's `Service`.
- Implement the shared aggregate deadline normalizer used by 05, 06, 09, and the sweeper.
- Own no root constructor, ports, policy declarations, schema, store files, HTTP handlers, or bootstrap files.
- Request dependency/constructor changes from 01/05 and persistence changes from 04; do not edit their files.

## Required behavior

1. Use only injected UTC clock values. Defaults are heartbeat 15s, lease 45s, grace 60s, sweep 5s.
2. A lease belongs to `(lobby, player, membership_generation, client_instance_id)`; it never consumes another seat.
3. Operate only on the principal's membership. Neither client IDs nor generations grant authority over another player.
4. A live lease has `expires_at > now` and has not been released. Exact expiry is not live.
5. Each current lease's effective expiry is `min(expires_at, released_at)` if released, otherwise expires_at; grace ends at their maximum plus 60s.
6. Never set the grace deadline to `now + grace` merely because the sweeper first discovers an expired lease.
7. `disconnected_until` describes the reserved membership during grace; clear it when a current lease becomes live.
8. Preserve host, team, and ready during grace. Grace counts against room/team capacity, but cannot satisfy start's live-lease rule.
9. Heartbeat may extend an expired, unreleased lease during grace to `now + lease_duration`.
10. Heartbeat cannot reopen a released tuple, revive membership after grace, or update an older generation.
11. Explicit release affects only this client tuple. Another live lease keeps the same membership connected.
12. Repeated release of the same released tuple is successful without changing its effective release time or emitting an event.
13. Old-generation heartbeat/release returns `stale_membership` (409), even when its lease row still exists.
14. Unknown current-generation heartbeat returns 409 stale_membership; unknown current-generation release is an idempotent 204. Check generation first. Do not create a lease from heartbeat.
15. Only explicit create/join creates client leases or reactivates inactive membership.

## Implementation steps

1. Implement the pure aggregate normalizer first; return the frozen `Change` classification and revocation intents.
2. Derive due deadlines from current-generation lease records, retaining enough history to handle an entirely released generation.
3. On waiting-room expiry, deactivate members, clear live team/ready, release remaining current leases, and elect host by join_sequence then ID.
4. Close a waiting lobby when no active members remain. Never reopen a closed lobby.
5. On started-room expiry, change live access state while preserving every immutable start-roster entry.
6. Expiring multiple members in one locked normalization increments the lobby version once and emits one invalidation.
7. Emit a revocation intent for each deactivated player/generation; do not contact Centrifugo here.
8. Use 05's `runMutation`/exported TransactNormalized helper; build the response from its post-commit aggregate: normalize the loaded aggregate, execute the command on a deep copy, and commit within one room-locked transaction.
9. Command failure discards its copy but commits normalized state; return the captured domain error after commit, with database failure taking precedence.
10. Heartbeat extensions alone are private lease writes; visible connected/grace/inactive transitions follow the public-change contract.
11. Implement a bounded sweep pass: query due scopes, transact each scope independently, and keep processing other scopes after one failure.
12. Prevent a permanently failing first batch from starving other due scopes; coordinate cursor/backoff behavior with 04 if necessary.
13. Run the loop using an owned timer/context; cancellation stops scheduling work and returns control to bootstrap.
14. Expose a single-pass entry point for deterministic tests. Do not require real sleeps to trigger deadline behavior.
15. Keep network I/O and game callbacks out of mutations. A second worker must be harmless because each scope is locked.

## Named tests and exact assertions

- `TestHeartbeatDuringGracePreservesMembership`: same member ID/generation/team/ready/host; extends only the selected lease.
- `TestHeartbeatAtGraceBoundaryRequiresJoin`: at the exact boundary cleanup commits, heartbeat fails, and no lease/member is revived.
- `TestLeaseBoundaryIsStrict`: one instant before expiry is live; at expiry start is disallowed while membership remains reserved.
- `TestReleasedLeaseCannotBeReopened`: release twice retains the first effective deadline; later heartbeat on that tuple fails.
- `TestReleaseAfterExpiryDoesNotExtendGrace`: releasing an already expired lease never pushes the prior departure deadline into the future.
- `TestTwoClientsShareOneSeat`: one client releases/expires while another remains live; host, team, readiness, and membership persist.
- `TestOldGenerationCannotTouchRejoinedMember`: delayed heartbeat and release both fail 409 and leave new-generation leases untouched.
- `TestRestartUsesOriginalGraceDeadline`: a late sweep removes overdue membership immediately; no fresh grace is granted.
- `TestGraceRetainsCapacity`: full room and full team remain full until the durable grace deadline passes.
- `TestExpiryElectsHostDeterministically`: simultaneous expiries pick lowest surviving sequence, with ID tie-break; last waiting member closes room.
- `TestStartedExpiryPreservesRoster`: inactive state changes while the frozen roster is byte-for-byte equivalent and room stays started.
- `TestDeniedCommandStillCommitsExpiry`: normalization's version/revocation commit survives the requested mutation returning an error.
- `TestUnknownLeaseTuplePolicy`: unknown current-generation heartbeat is 409, release is 204/no mutation; old generation is 409 for both.
- `TestHeartbeatResponseUsesCommittedVersion`: visible grace-to-connected change returns the new persisted version.
- `TestLeaseOnlyHeartbeatDoesNotPublish`: repeated live heartbeats update expiry without incrementing version or inserting invalidation.
- `TestConcurrentHeartbeatAndSweepSerialize`: barrier-driven independent PostgreSQL connections produce a valid renewal or expiry outcome, never both.
- `TestTwoSweepersDoNotDuplicateExpiry`: each departed generation has one effective transition; no duplicate version increment.
- `TestSweepCancellationAndPerRoomFailure`: canceled work exits; one failed scope neither rolls back other rooms nor permanently starves them.

## Verification

```sh
go test ./lobby/... -run 'Test(Heartbeat|Lease|Release|Released|TwoClients|OldGeneration|Restart|Grace|Expiry|StartedExpiry|DeniedCommand|ConcurrentHeartbeat|TwoSweepers|Sweep)' -count=1
go test -race ./lobby/... -run 'Test(ConcurrentHeartbeatAndSweepSerialize|TwoSweepersDoNotDuplicateExpiry)' -count=1
```

PostgreSQL cases use 02's disposable fixture and barriers. No developer database or SQLite substitute.

## Done and handoff

- Give 05/06/09 `NormalizeExpired` usage inside `runMutation`, with one successful and one denied-command example.
- Give 13 the sweeper constructor, cancellation contract, timing settings, and single-pass test entry point.
- List changed files, commands/results, exact clock boundary assertions, and dependencies not yet integrated.
- Report any mismatch with the frozen unknown-tuple/error policy; do not invent a local alternative.
