# 18 — End-to-end acceptance and final documentation

Prerequisites: tasks 01–17 integrated. This task proves the intended outcome with actual services and the generated Python client. Unit mocks alone cannot complete it.

## Exclusive ownership

Create `lobby/integration/` (isolated Compose/test launcher and Python acceptance tests) and `scripts/test-lobby-acceptance.sh`. Own final updates to root `README.md`, `backend/README.md`, and `lobby/docs/multiplayer-architecture.md`. Do not edit generated Python or other agents' implementation files silently; coordinate failures back to owners. Existing development Compose belongs to 13; use a separate test project/ports/volumes here.

## Steps

1. Create an ephemeral integration environment with disposable PostgreSQL, the pinned Centrifugo version from 13, and the built backend. Use generated test credentials and explicit URLs, not the developer's database. Wait on meaningful health checks, with deadlines and logs on failure. Always clean test-owned processes/containers/volumes.
2. Install the locked Python package and run tests through its public wrapper/generated commands. No direct handcrafted HTTP calls for happy-path commands; raw adversarial requests may test denied auth/proxy cases.
3. Two guests: create island-battles room, join by code, select separate teams, set ready, start, call the Island adapter through an appropriate test API helper, and verify one linked game. Repeat generic room operations for quiz-demo; no Island game should appear.
4. Test reconnect by disrupting socket transport while keeping the client process/credential. Restore before grace and verify same player/member/team/host/seat, fresh snapshot, and no extra lobby/game. Do not simulate this solely by calling join twice.
5. Force history loss (test Centrifugo restart or deliberately short isolated test retention) and mutate room state while a client is away. Verify recovery failure converges to authoritative snapshot. Control membership grace separately so the intended member is still authorized.
6. Exercise a state change between subscription acknowledgement and initial snapshot completion through test synchronization hooks; confirm the client eventually displays the latest version. Also delay/fail outbox publish while HTTP commits; periodic reconciliation must still converge, and retried delivery must not duplicate state.
7. Test multi-client same credential. Releasing one client does not expire the other's seat. Expire all leases past grace; restart backend before sweep, then confirm persisted deadline cleanup and correct host transfer. Check started-room frozen roster rejoin independently from waiting-room expiry.
8. Test isolation: player B cannot read/subscribe A's unrelated room or private channel; bad-signature connection JWT fails; forged internal proxy call without secret fails; former member cannot obtain snapshots. Confirm client presence/history/publish are denied and no roster appears in invalidation history.
9. Start the backend against a disposable empty database, initialize the schema, restart it against the same database, and verify initialization remains repeatable: current tables are unchanged and no retired player, room-name, or archive tables appear.
10. Run exact reproducibility check from 15 in a clean checkout/temp output. All public operations and LobbyInvalidation model must generate, with no warnings, skipped endpoints, or diff. Verify root-level lobby dependency graph contains no backend imports.
11. Update documentation to describe only completed behavior, installation/generation/test commands, session expiry, team/host rules, recovery limits, fresh-schema startup, and the explicit Island adapter call. Keep Lambda/C#/TypeScript/round timers clearly pending. Link new package/client docs and preserve useful original architecture research.

## Mandatory acceptance tests

| Test | Required observation |
| --- | --- |
| `test_two_python_players_create_join_choose_teams` | Generated HTTP calls and real subscriptions yield identical latest rosters |
| `test_generic_second_game_has_no_island_dependency` | quiz-demo room works; no Island game row or import dependency |
| `test_socket_reconnect_preserves_membership` | Real transport loss before grace preserves IDs/team and creates no game |
| `test_history_loss_reconciles_snapshot` | Actual unavailable history/epoch change still reaches correct latest version |
| `test_subscribe_snapshot_race` | Event during blocked snapshot request is not lost |
| `test_commit_with_publish_failure` | State commits, outbox remains retryable, client converges before/after replay |
| `test_multiclient_release_and_restart_expiry` | One close preserves other lease; overdue deadline survives backend restart |
| `test_leave_revokes_state_access` | Snapshot and new subscriptions denied; late wire events contain only frozen invalidation fields |
| `test_wrong_identity_and_proxy_spoofing` | Bad JWT/cross-room/private access/forged proxy rejected with no protected data |
| `test_schema_initialization_is_repeatable` | Fresh-schema startup and restart leave current tables usable without retired tables |
| `test_generated_client_is_reproducible` | Fresh generation exactly matches tracked output and imports successfully |

## Final verification commands

From repository root: `go test ./...`, `go vet ./...`, focused `go test -race ./lobby/... ./backend/lobbyadapter/... ./backend/dal/...`, the Python locked unit-test command from 15/16, generation check, and `./scripts/test-lobby-acceptance.sh`. Large existing game suites require Docker. Do not silently skip Docker tests or call unrun tests successful.

The final handoff lists commands/results, tested Go/Python/Centrifugo/PostgreSQL/generator versions, schema initialization behavior, generated SDK path, example command, and any unresolved integration failure. Task 18 is incomplete until the live two-player flow, reconnect fallback, and clean generation work; documentation alone is not sufficient.
