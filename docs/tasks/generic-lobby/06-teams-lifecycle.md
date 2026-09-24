# 06 — Team selection, readiness, host transfer, start, and leave

Prerequisites: 01/04 for development, 05/07 for final service integration. Follow the contract; do not invent auto-balancing, auto-ready, team-color allocation, or host reassignment on a transient disconnect.

## Exclusive ownership

Create `lobby/application/teams.go`, `lifecycle.go` and their tests. Use the Service and runMutation helper owned by 05. Do not alter domain types/ports, schema, HTTP DTOs, game simulation or auth. Request shared changes from their owners.

## Steps

1. Implement SelectTeam under the room transaction. Require active membership, waiting phase, and target team in this lobby. Nullable target means clear selection. Check target capacity before releasing the old team so failure preserves the old selection.
2. Selecting the current team, including null when already unassigned, is a no-op. A real change resets ready to false and generates one visible change. Never use GORM Updates(struct) in a way that drops false/NULL; actual persistence belongs to task 04.
3. Implement SetReady as desired state. Ready=true requires team assignment; ready=false is permitted unassigned. Both require active membership and waiting phase. Repeated value is a no-op.
4. Implement Start with host, phase, minimum two active players, all assigned/ready, and a live lease for every active member. A member only in grace blocks start. Empty teams are valid. Capture immutable StartRoster with stable team order in the same transaction as phase/version.
5. Started prevents any further team/ready changes. A repeat start from current host returns current snapshot without another event/roster. Generic start has no game/backend/Lambda side effect.
6. Implement Leave with membership lookup including inactive historical rows. An outsider gets not_found; an already inactive former member succeeds without mutation. An active leave deactivates membership, clears live team/ready, releases current-generation leases, and adds revocation intent carrying that generation.
7. Transfer host to lowest active join_sequence then ID. Grace-reserved members remain active and eligible. Close a waiting room if none remain; never reopen it. In a started room with no active members, host becomes null, phase stays started and StartRoster remains available for original players' rejoin.
8. Ensure start/team change/leave/expiry all use the same room lock and common normalization helper. A denied action must not preserve overdue membership by rolling back normalization. Errors must not leave a partially freed team or partially written roster.
9. Share leave/host transition domain logic with 07 through frozen helpers rather than making the sweeper call a public authenticated endpoint. Keep all side effects represented as Change records.

## Required tests

| Test | Arrange and assert |
| --- | --- |
| `TestSelectLastTeamSeatConcurrent` | Two members race for last target slot; exactly one moves; loser retains prior team/readiness |
| `TestTeamSelectionRejectsOtherRoomTeam` | Valid UUID from another lobby cannot be assigned; state/version unchanged |
| `TestTeamSelectionRequiresMembership` | Authenticated outsider cannot inspect/change teams, including supplying another player ID |
| `TestTeamChangeClearsReady` | Ready member switches/clears team; ready=false persists in PostgreSQL |
| `TestRepeatedTeamSelectionIsNoOp` | Same team twice leaves version and event count unchanged |
| `TestReadyValidation` | true without team fails; false without team succeeds; missing readiness input tested in 08 |
| `TestStartRequiresHostAndLiveReadyRoster` | Nonhost, one player, unassigned, unready, or grace-only member blocks start; valid roster succeeds |
| `TestStartAndTeamChangeSerialize` | Race operations; result is a legal serial ordering, never started roster inconsistent with selected teams |
| `TestRepeatedStartIsNoOp` | Same host retries; one immutable roster and one start-related version change |
| `TestHostLeaveTransfersDeterministically` | Host leaves with two candidates; fixed successor independent of DB row order |
| `TestLastWaitingMemberLeaveClosesRoom` | Last leave closes and nulls host; code cannot be joined/reopened |
| `TestStartedLeavePreservesRoster` | All members leave started room; roster immutable, host null, original identity may rejoin |
| `TestLeaveIsIdempotentAndEmitsRevocation` | First leave produces one revocation for old generation; retry no duplicate event/job |
| `TestLeaveAndJoinCapacityRace` | Concurrent release/join never overfills or loses a seat under real PostgreSQL locks |

## Verification and handoff

Run lifecycle/team tests on disposable PostgreSQL and the integrated race suite. Assert final persisted state, version, roster and outbox records, not only returned success. Report frozen team order to task 12 and phase/error examples to 08/16. No changes to castle/soldier TeamColor belong in this task.
