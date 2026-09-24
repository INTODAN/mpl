# Cross-task edge cases and test ownership

This matrix prevents seams between otherwise passing tasks. Individual task files specify detailed named tests. Use real PostgreSQL for constraints/concurrency/schema initialization, fake time for deadlines, protocol fixtures for proxy shape, and real Centrifugo/Python for recovery/auth compatibility.

| Risk or boundary | Required outcome | Primary tasks |
| --- | --- | --- |
| Connect/reconnect has create/join data | Connection has no create/join/game side effect | 03, 09, 13, 18 |
| Root lobby tests import backend/internal | Independent root-level lobby fixture; no backend dependency | 01, 02, 18 |
| Same display name or code known | Neither implies authentication or player ownership | 03, 08, 18 |
| Bearer expires while socket is active | Token refresh fails; new auth fails; no new guest silently created | 03, 09, 16 |
| Proxy body/headers claim another user | Trust only server-authenticated proxy plus JWT user; reject forged secret | 09, 13, 18 |
| Same actor retries create after timeout | Same request ID/payload gives original response and one lobby | 04, 05, 15, 16 |
| Same request ID with different body | Conflict, no second lobby | 04, 05, 08 |
| Join code collision/exhaustion | Bounded retries, atomic state, controlled error | 04, 05 |
| Unknown code returns nil record | Not_found; never join success or panic | 04, 05, 08 |
| Two players compete for last lobby/team seat | Exactly one success; aggregate never over capacity | 04, 05, 06 |
| One identity has multiple devices | One member/seat; independent leases | 05, 07, 16, 18 |
| Team belongs to other lobby/game | Reject, old selection intact; composite FK protects direct write | 02, 06, 08 |
| Team null vs omitted; false vs omitted | null clears and false unsets; omitted required field is invalid | 04, 06, 08, 15 |
| Team change while ready/start races | Serialize; clear ready; frozen roster consistent | 04, 06 |
| Host disconnects briefly | Keep host through grace; cannot start with grace-only players | 06, 07 |
| Host expires/leaves with several candidates | Deterministic active successor, no double transfer | 06, 07 |
| Last member leaves waiting vs started | Waiting closes; started keeps immutable roster and can rejoin | 05, 06, 07 |
| Delayed old heartbeat/release after rejoin | Generation fence rejects; new lease unaffected | 05, 07 |
| Release arrives after natural expiry | Does not extend grace | 01, 07 |
| Every lease explicitly released | Defined persisted deadline; no empty-max permanent seat | 01, 07 |
| Backend restart after deadline | Deadline uses persisted effective expiry; no renewed grace | 07, 13, 18 |
| Rejected action coincides with due expiry | Commit cleanup only, return action error; no partial action state | 04, 05, 06, 07 |
| DB commit fails | No invalidation/revocation or reported successful mutation | 04, 05, 10 |
| Commit succeeds, publish fails | Durable retries plus snapshot reconciliation; duplicate-safe | 04, 10, 16, 18 |
| Two outbox workers or abandoned claim | Claim deadline reclaim; stale claim cannot ack other's job | 04, 10 |
| Leave→rejoin races unsubscribe retry | Skip obsolete generation; transient unsubscribe triggers safe resubscribe | 09, 10, 16 |
| Departed socket briefly remains subscribed | Only invalidations; no roster/presence/history API leak | 09, 13, 18 |
| Snapshot read overlaps mutation | Version and children from one consistent transaction | 04, 05 |
| Initial snapshot overlaps publication | Subscribe acknowledgement first, buffered max version, refetch | 16, 18 |
| Event duplicate/out of order/dropped | Monotonic accepted snapshot; periodic reconciliation | 10, 16, 18 |
| History missing or Centrifugo restart | Re-subscribe, authorized snapshot, no seat/game duplication | 13, 16, 18 |
| SDK callback waits on HTTP/SDK | Callback only queues; reader remains responsive | 16 |
| Signal queue fills during reconnect burst | Coalesce highest version; bounded memory without lost dirty state | 16 |
| Python close/cancel while snapshot pending | Cancel owned work only, no unrelated asyncio tasks, no task leak | 16, 17 |
| Generated SDK drops nullable field/error route | Fail generation on warnings; schema/import/request tests | 14, 15 |
| Unreferenced realtime model omitted by generator | Explicit component/model assertion; no hand-maintained duplicate | 08, 14, 15 |
| Export command needs DB or secrets | Pure route registration export with inert dependencies | 08, 14 |
| Island adapter retried/concurrent | One game per lobby and stable color map from frozen roster | 12, 18 |

## Deterministic race setup

For capacity tests, seed a nearly full room, open separate DB sessions, synchronize goroutines immediately before competing service calls, and await both results. Assert persisted membership/team counts, versions and outbox rows. Run the same test with reversed contenders. Do not infer correctness from an absence of data races in Go; database locking is the actual property.

For time tests, inject a mutable test clock and pass explicit timestamps. Exercise `deadline-1ns`, `deadline`, and `deadline+1ns`. Instantiate a second service/sweeper against the same test DB to model restart. Use real wall-clock waits only in bounded live transport tests where the transport cannot use the fake clock.

For client synchronization, gate snapshot responses with asyncio events and deliver publication signals before/during/after the response. Test a snapshot older than the highest buffered version and a late response older than the latest accepted snapshot. The final accepted snapshot must be newest, with bounded workers/queues.

## Regression gate

Existing generation, soldier-group, and graph tests must keep passing alongside the lobby extraction. Visual graph export is not required for a lobby-only change; use the existing game-state-testing skill if a simulation regression needs investigation. Do not rewrite game expectations to hide extraction errors.
