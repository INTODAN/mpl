# Task 16 — Implement the asyncio lobby connection wrapper

Status: implemented; see [verification evidence](implementation-status.md). Prerequisite: task 15's installed generated SDK and import-name handoff.
Read [README](README.md), [architecture](architecture.md), and [contracts](contracts.md).
Use invalidations plus authoritative snapshots; publications do not contain patches or full state.

## Exclusive ownership

- `lobby/clients/lobby-python/lobby_client/__init__.py`, `client.py`, `reconcile.py`, and `errors.py`.
- `lobby/clients/lobby-python/tests/test_reconcile.py` and `tests/test_client_lifecycle.py`.
- No generated code, dependency/lock files, examples, or user README edits; request task 15 changes.
- Publish stable wrapper signatures to task 17 before it starts its CLI implementation.

## Public API and resource ownership

1. Export `LobbyClient` with async context management and contract operations: create_session,
   create_lobby, join_lobby, select_team, set_ready, start, snapshot, leave, and close.
2. Expose `updates(scope)` as an async iterator yielding accepted generated Snapshot objects.
3. Surface terminal errors both to that iterator and pending initial synchronization callers.
4. Accept base HTTP URL, optional retained guest credential, and injectable HTTP/SDK/clock dependencies.
5. Expose newly created session credentials to the caller explicitly; never persist or log them implicitly.
6. Allocate a new client-instance UUID per wrapper lifetime; reuse it for its create/join/heartbeat/release.
7. Support several joined scopes with per-scope state; one reconciliation worker handles their signals.
8. Own one generated HTTP client, one SDK client, bounded reconciliation signals, and heartbeat scheduling.
9. Reuse generated models and async detailed HTTP calls; translate problem codes to typed wrapper errors.
10. Constructors create no network activity; explicit session/create/join or async startup performs I/O.
11. Create requires a caller-retained request_id; retry only with the identical validated request payload.
12. Never automatically create a guest or join a lobby from socket callbacks, retries, or missing credentials.

## Subscription and snapshot algorithm

- Obtain the WebSocket URL and connection JWT from `createLobbyConnectionToken` using the guest bearer.
- Supply SDK connection `get_token` provider that calls that same generated operation for renewal. This token provider is allowed to await HTTP; the fast-callback rule applies to event callbacks. Translate terminal 401/session expiry to `centrifuge.UnauthorizedError` and surface the wrapper error; generic exceptions are treated by the SDK as transient retries.
- Supply no subscription token or subscription `get_token`; membership is checked by server proxies.
- Explicit create/join returns scope/channel; install callbacks, initiate subscription, then await `ready`.
- Do not treat `connect()` or `subscribe()` returning as successful connection/subscription acknowledgement.
- Every acknowledgement enqueues reconciliation, including successful recovery and repeated subscriptions.
- Callbacks validate event scope/schema and coalesce the highest signaled lobby version, then return.
- Maintain per-scope accepted_version, target_version, dirty flag, active generation, and lifecycle epoch.
- Before fetching, consume the current dirty flag; signals arriving during HTTP I/O set it again.
- Worker accepts only matching-scope snapshots with version >= the last accepted version.
- Install state atomically; publish an update on first state or a greater accepted version.
- If snapshot.version < target_version, fetch again with bounded backoff rather than busy spinning.
- A dirty signal during the request schedules another fetch even if it contains no version hint.
- Keep a set of dirty scopes and a wakeup event, not an unbounded queue of every invalidation.
- An old snapshot completion after leave/rejoin/close cannot install state: compare lifecycle epoch first.
- Merge command-returned snapshots using the same nondecreasing-version rule; they cannot roll back state.
- Wrong-lobby events never alter state; unknown schema_version surfaces compatibility failure for that scope.
- Never enforce contiguous invalidation versions: coalescing and out-of-order delivery are valid.
- Centrifugo offsets/epochs are transport metadata, not application lobby versions.
- Retry transient snapshot/network/503 failures with bounded exponential backoff; reset after success.
- A failed recovery or epoch change requests reconciliation; every acknowledgement already does so.
- Reconcile at least every 15 seconds while subscribed, even with no publication or version change.

## Liveness, revocation, and shutdown

- While SDK-connected, heartbeat every 15 seconds with the current generation and client-instance UUID.
- Pause heartbeat while SDK-disconnected; socket connection alone does not renew application membership.
- Compare heartbeat response version to accepted state and enqueue reconciliation when ahead.
- Heartbeat stale_membership stops the old lease; require explicit join rather than silently reacquiring it.
- On server unsubscribe, worker checks membership by snapshot; authorized scopes resubscribe with backoff.
- Stop a scope on confirmed 404; stop authentication-dependent work on confirmed 401/session expiry.
- A revocation racing with a newer membership must recover via authorization/resubscription, not auto-leave.
- SDK owns network reconnect/backoff; do not implement a second WebSocket transport or reconnect loop.
- Leave deactivates the identity across clients; invalidate local in-flight work and remove that scope.
- Close first stops new work/heartbeats, then best-effort releases each owned lease with bounded timeout.
- Close unsubscribes/disconnects transports and cancels/awaits only wrapper-owned tasks; make it idempotent.
- Release failures cannot prevent local cleanup; close does not issue leave or erase other clients' leases.
- Propagate cancellation after cleanup and retrieve background-task exceptions; never cancel `all_tasks()`.
- Publication/subscription/lifecycle event callbacks perform no slow HTTP or awaitable SDK operations, and no exception may escape them. The connection-token provider follows the separate HTTP/error rules above.
- Slow update consumers cannot block callbacks; coalesce their pending snapshot to the newest version.

## Required tests and exact assertions

Use fake SDK events, MockTransport, barriers, and injected time; no real sleeps or live services.

- `test_initial_ack_before_snapshot`: snapshot HTTP starts only after subscription acknowledgement.
- `test_event_during_initial_fetch`: event V6 during snapshot V5 forces a second fetch and reaches V6.
- `test_duplicate_out_of_order_signals`: signals 7, 6, 7 coalesce; state never rolls backward.
- `test_late_command_response`: command V5 after snapshot V7 does not replace V7.
- `test_ack_during_fetch`: versionless re-acknowledgement is not lost while a snapshot is outstanding.
- `test_leave_rejoin_epoch`: old in-flight response cannot overwrite new membership-generation state.
- `test_signal_flood_bounded`: many duplicate events consume bounded memory and preserve highest version.
- `test_recovered_and_failed_recovery`: each acknowledgement triggers fresh snapshot for either flag.
- `test_periodic_reconcile`: absent publications still yield authoritative refresh at the 15-second boundary.
- `test_heartbeat_connection_gate`: disconnected clients stop heartbeat; connected clients send exact generation.
- `test_heartbeat_version_and_stale`: newer version reconciles; stale generation never auto-joins.
- `test_token_provider_terminal_denial`: terminal HTTP401 raises SDK UnauthorizedError and stops reconnect retries; transient HTTP503 remains retryable.
- `test_token_refresh_identity`: connection token renewal uses the original bearer and creates no guest.
- `test_no_subscription_jwt`: subscription construction never supplies a token or token-refresh callback.
- `test_auth_denied_vs_transient`: 401/404 terminates appropriate work; network/503 retries with backoff.
- `test_server_unsubscribe_reauthorize`: still-authorized room resubscribes; former membership stops.
- `test_unknown_schema_and_wrong_scope`: compatibility error is surfaced; unrelated state is untouched.
- `test_explicit_retry_policy`: no mutation retry invents a request_id or repeats non-idempotent work.
- `test_cancel_each_wait`: cancel ready, snapshot, refresh, and backoff; owned tasks/resources terminate.
- `test_close_ownership`: unrelated asyncio task survives; only own leases release; no leave call occurs.
- `test_close_twice_and_release_failure`: repeated close and unavailable release still finish cleanup.
- `test_two_scopes_and_slow_consumer`: scopes stay isolated and SDK callbacks continue returning promptly.

## Verification and handoff

```sh
uv run --project lobby/clients/lobby-python --locked pytest lobby/clients/lobby-python/tests/test_reconcile.py lobby/clients/lobby-python/tests/test_client_lifecycle.py
```

Report signatures, test results, token/subscribe behavior, and owned-resource cleanup evidence.
Task 18 owns actual server recovery acceptance; fake SDK tests do not establish live compatibility.
[Pinned SDK source](https://github.com/centrifugal/centrifuge-python/tree/0.6.0) exposes ready/recovery hooks.
[SDK callback guidance](https://github.com/centrifugal/centrifuge-python) warns callbacks block SDK reads;
the current README may be newer than the pin, so verify behavior against the installed 0.6.0 source.
