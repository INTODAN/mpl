# Task 17 — Add a runnable lobby CLI and user documentation

Status: implemented; see [verification evidence](implementation-status.md). Prerequisite: task 16's wrapper and documented public signatures.
Read [README](README.md), [architecture](architecture.md), and [contracts](contracts.md).
This task demonstrates the real generated HTTP client and official SDK through the wrapper.

## Exclusive ownership

- `lobby/clients/lobby-python/examples/lobby_cli.py` and `examples/__init__.py` if needed.
- `lobby/clients/lobby-python/README.md` and `tests/test_cli.py`.
- No edits to generated sources, wrapper internals, packaging/locks, or backend/configuration files.
- Request task 15/16 owner changes if a missing dependency or public wrapper API blocks the example.

## CLI behavior

1. Implement an argparse-based executable launched from repository root with the locked project.
2. Require `--base-url`; allow `--game-key` defaulting to `island-battles`.
3. Require exactly one mode: `--create NAME` or `--join CODE`.
4. Require `--session-file PATH` for explicit credential retention across client process restarts.
5. Creating a guest requires explicit `--new-session DISPLAY_NAME`; never fall back to a new identity.
6. If the session file already exists, reject `--new-session` without overwriting that credential.
7. Load an existing credential when `--new-session` is absent; missing/unreadable/malformed files fail clearly.
8. Store only player_id, access_token, and expires_at, using a temporary sibling file and atomic rename.
9. Create session files with owner-only read/write permissions on the supported local POSIX platform.
10. Never print tokens, raw session files, authorization headers, or SDK token-bearing debug logs.
11. For `--create`, support `--capacity` default 4 and two default teams named Red and Blue.
12. Default each team's capacity to lobby capacity; generic labels have no Island Battles color semantics.
13. Allocate one create request UUID and retain it for any explicit same-process retry of that command.
14. For `--join`, preserve the game-key/join-code scope; do not guess a lobby by display name.
15. Print the initial synchronized snapshot: scope, join code, phase, host, ordered teams/members, version.
16. Keep the process alive to receive `updates(scope)`; display newer snapshots as they arrive.
17. Present commands `team N`, `team none`, `ready`, `unready`, `start`, `show`, `leave`, and `quit`.
18. Resolve team N using the displayed immutable team order, then send its UUID to the wrapper.
19. `team none` sends explicit null; `unready` sends false; empty/unknown commands change nothing.
20. `leave` explicitly calls wrapper leave and exits; `quit`, EOF, and Ctrl-C only close/release this client.
21. Clearly state that leave affects the guest identity across all of its running clients.
22. Handle expected full/team-full/not-ready/not-host/phase errors as readable command failures.
23. Terminal expired-credential or lost-membership errors explain the required explicit new session/join.
24. Never retry create with a fresh request ID or mint another guest in response to connectivity failure.
25. Support asynchronous stdin without blocking the event loop or leaving an uncancellable input thread.
26. A POSIX `loop.add_reader` implementation is sufficient; remove its reader during every exit path.
27. Document the example's POSIX terminal requirement if no other console adapter is implemented.
28. Ensure SIGINT/SIGTERM, EOF, iterator failure, and command exception all run wrapper cleanup.

## README content and runnable walkthrough

- Explain generated `generated/` versus handwritten `lobby_client/`; name Huma as schema authority.
- List Python >=3.14, uv, PostgreSQL/backend/Centrifugo prerequisites and link task 13's actual startup docs.
- Show locked install and `scripts/generate.py --check`; do not tell users to hand-edit generated files.
- Show two terminal commands with different session files: one creates, the other joins its printed code.
- Explain `team 1`/`team 2`, `ready`, and host `start`; state that start freezes roster only.
- State that the CLI neither invokes Lambda nor generates Island Battles maps/simulation.
- Demonstrate `quit` then explicit join with the same session file and a new client-instance UUID.
- Explain the 45-second lease and 60-second grace, and that close releases this client's lease early.
- Describe forced network interruption separately: SDK reconnects automatically while membership survives.
- Explain heartbeat pauses during disconnect and rejoin is explicit after membership expiration.
- Explain using one credential from two clients creates two leases but consumes only one seat.
- Include API base URL troubleshooting, expired session behavior, and typed command conflict messages.
- Document no automatic identity recovery after the 24-hour guest credential expires.
- Mark live smoke/reconnect claims as pending until task 18 records passing evidence.

## Required tests and exact assertions

Use fake wrapper/session I/O and injected stdin; do not need backend or Centrifugo.

- `test_mode_validation`: missing/conflicting create/join flags fail before network activity.
- `test_existing_session_preserved`: new-session refuses an existing credential file without modifying it.
- `test_missing_credential_no_fallback`: missing/malformed file never invokes create_session implicitly.
- `test_session_permissions_and_redaction`: session is owner-only and output never contains bearer/token data.
- `test_team_order_and_null`: displayed team order resolves correct UUID; none passes Python None.
- `test_ready_false`: unready passes False unchanged to the wrapper.
- `test_create_request_id_stable`: an allowed retry preserves request UUID and exact create parameters.
- `test_commands_and_updates`: queued user commands and incoming snapshots both progress without blocking.
- `test_quit_eof_and_signal`: all invoke close once and never invoke leave.
- `test_leave_explicit`: leave calls identity-wide leave before final cleanup.
- `test_terminal_error_cleanup`: iterator/authentication failures close resources and exit nonzero.
- `test_unknown_command`: invalid input prints usage and sends no mutation.

## Verification commands

```sh
uv run --project lobby/clients/lobby-python --locked pytest lobby/clients/lobby-python/tests/test_cli.py
uv run --project lobby/clients/lobby-python --locked python lobby/clients/lobby-python/examples/lobby_cli.py --help
uv run --project lobby/clients/lobby-python --locked python lobby/clients/lobby-python/examples/lobby_cli.py --base-url http://localhost:8888 --session-file /tmp/lobby-alice.json --new-session Alice --create Demo
uv run --project lobby/clients/lobby-python --locked python lobby/clients/lobby-python/examples/lobby_cli.py --base-url http://localhost:8888 --session-file /tmp/lobby-bob.json --new-session Bob --join PRINTED_CODE
```

The last two commands require the live stack and separate terminals; replace PRINTED_CODE with actual output.
Use fresh test session paths; do not overwrite a user's existing retained credential while demonstrating.

## Completion evidence

Report CLI/test files, help output, unit-test results, and a sanitized two-client transcript if run.
Identify live checks still owned by task 18; do not describe a mock transcript as a live success.
Hand task 18 exact CLI commands and wrapper operation/cleanup behavior for acceptance automation.
