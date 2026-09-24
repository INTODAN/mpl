# 05 — Creation, joining, and authorized snapshots

Prerequisites: 01, 03, 04. Final tests also integrate NormalizeExpired from 07. Read `backend/handlers/connect.go` and `backend/dal/game.go` for old bugs that must disappear. Do not call them from the new service.

## Exclusive ownership

Create `lobby/application/service.go`, `membership.go` and corresponding external-package service tests. Task 06 owns team/lifecycle methods; 07 owns leases/sweeper/NormalizeExpired. You own the shared Service struct, constructor and runMutation helper; communicate dependency changes rather than editing one another's files.

## Steps

1. Implement the frozen constructor and injected dependencies. Validate configured registered games and defaults. Service logic receives authenticated Principal; it does not parse headers or issue credentials.
2. Implement exported TransactNormalized and a thin Service.runMutation wrapper exactly as contracts specify; task 09 must reuse the exported helper rather than an inaccessible method. Capture one UTC now within each locked callback; call NormalizeExpired; deep-copy before executing command; commit only cleanup if command rejects. Merge successful changes into one visible version increment. A DB error supersedes captured command error. No publish/network call inside callback.
3. Create validates game/name/capacity/team definitions and client UUID before generating aggregate IDs. Host is the principal, first membership is active generation 1, join_sequence 1, team null, ready false, and one 45-second lease exists. Never initialize a game/generation here.
4. Pass validated create request to atomic store Create. Fingerprint canonical validated values, including ordered teams and client ID; idempotency key is principal+request_id. A collision retries with fresh join code, maximum five attempts. Randomness and database errors must not leave a partially joined room.
5. Join normalizes code and resolves it within the provided game key. Under lobby lock, apply current deadlines before checking capacity/phase. Count active memberships, including members whose lease expired but grace has not. Unknown code, scope mismatch, and closed rooms cannot join.
6. Already-active join is idempotent: retain member ID, generation, team, ready, host and join sequence; add/renew this client lease only. A released client tuple cannot be revived; require a new client UUID. No extra visible event for a lease-only join.
7. Waiting-room rejoin of an inactive member reuses member ID, increments generation, allocates/increments the room next_join_sequence under lock, clears team/ready, and establishes lease. A newer generation is mandatory so old heartbeats/releases/revocations cannot affect it.
8. Started-room rejoin admits only identities in StartRoster, restores their frozen team, sets ready=false and a new generation if inactive, and does not modify the roster. If HostID is null (no active host), this member becomes host; a host in grace remains host. Non-roster identities receive 404 not_found without roster disclosure.
9. Snapshot uses the shared locked normalization/auth path and returns only active-member-visible data. Build responses from the aggregate returned after commit/version increment, with verified Principal.DisplayName for newly joined members and one aggregate version, with stable ordering and non-null arrays. Include own membership generation and canonical room channel; never expose bearer hashes, session IDs, raw leases or archived records.
10. Return the original stored response for an identical create retry; do not substitute a current snapshot containing data the original creator may no longer access. Explain this staleness to task 16, which subscribes/fetches after create.

## Required tests

| Test | Arrange and assert |
| --- | --- |
| `TestCreateTwoIndependentGames` | Create lobbies for island-battles and quiz-demo; correct game scope, creator seat/team definitions, no backend game rows created |
| `TestCreateRetryReturnsOriginalResponse` | Retry same actor/request; same lobby and response, one version-1 invalidation; changed payload gives 409-equivalent |
| `TestCreateRetryAfterCreatorLeavesLeaksNoNewRoster` | Change room after original creator leaves; replay returns only saved creation response |
| `TestCreateJoinCodeCollisionBounded` | Inject collisions; retry succeeds with fresh code, five exhausted collisions yield controlled error and no partial writes |
| `TestJoinLastSeatConcurrent` | Two principals race for one slot using real PostgreSQL/barrier; exactly one wins, count <= capacity |
| `TestJoinRetryDoesNotDuplicateMembership` | Same actor joins concurrently from two client instances; one member/seat, two leases, stable generation |
| `TestJoinWrongGameOrMissingCode` | A code from game A cannot join B; nil record is not success |
| `TestJoinCountsGraceReservations` | Temporarily disconnected member retains final slot until exact grace deadline |
| `TestLeaveRejoinReusesMembershipWithNewGeneration` | Rejoin inactive identity; member ID stable, generation/order increase, no unique violation |
| `TestStartedRejoinRestoresFrozenTeam` | Original roster member returns; new actor rejected, immutable roster unchanged |
| `TestResponseVersionMatchesCommittedVersion` | Successful create/join/snapshot uses persisted version and matching outbox version, never the pre-callback value |
| `TestSnapshotAuthorizationAndConsistency` | Outsider/old member cannot read; concurrent updates never pair old version with new children |
| `TestRejectedCommandStillCommitsDueExpiry` | Expired host attempts command; due cleanup/host transfer commits once, command errors, no partial command state |
| `TestRunMutationStorageFailureWins` | Force transaction failure after validation error; expose storage failure and no committed expiry/event |

## Verification and handoff

Run the named service tests with task 02 PostgreSQL helper, plus `go test -race ./lobby/...` when 06/07 are integrated. No sleeps for capacity races. Give 06/07 the exact shared helper and snapshot-builder usage; give 08 domain errors/status mapping examples. Do not mark this complete while NormalizeExpired is a placeholder or missing.
