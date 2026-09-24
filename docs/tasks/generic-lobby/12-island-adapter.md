# Task 12 — Extract game membership and bind started lobbies explicitly

Status: implemented; see [verification evidence](implementation-status.md). The thin `backend/lobbyadapter/` owns Island Battles service construction, endpoint registration, and binding tests. Shared binding helpers live under `lobby/`, game persistence under `backend/dal/`, and actual model types under `backend/models/`; the adapter defines no type aliases. The task breakdown below records the original implementation plan. Prerequisites: tasks 05/06 service behavior are available.
Read [contracts](contracts.md) and [architecture](architecture.md).
Read `backend/models/game.go`, `backend/dal/game.go`, `backend/dal/game_test.go`, and `backend/internal/testutils/test_generation.go`.
Also read `backend/models/castle.go`, `soldier_group.go`, `map.go`, and existing generation/service tests.
Apply `.agents/skills/backend-development/SKILL.md` and `.agents/skills/game-state-testing/SKILL.md`.

## Exclusive ownership

- `backend/models/game.go`, `backend/dal/game.go`, and `backend/dal/game_test.go`.
- New `backend/lobbyadapter/` implementation and tests, including adapter-specific Huma registration if needed.
- `backend/internal/testutils/test_generation.go` only if adapting game creation requires fixture changes.
- Game-specific team-binding persistence can live in `backend/lobbyadapter/`; export its records/initializer to task 13.
- Task 13 owns startup/dependencies/routes; request wiring edits from that owner.
- Task 02 owns generic tables. Do not edit its shared files.

## Outcome and boundaries

Island Battles retains game IDs, generations, map/castle/soldier/command state, and simulation-specific colors.
Active lobby membership and team selection live in the generic package.
An explicit authenticated `POST /game/from-lobby` creates or returns a game for a started `island-battles` lobby.
The adapter persists a stable generic-team-to-color map in one transaction with the game link.
Create/join/socket reconnect/start by themselves must create no game or generation.

## Model and DAL extraction

1. Keep `Game` primary key/timestamps and `Generations`; game persistence has no active player association.
2. Add nullable unique UUID `LobbyID`; detached existing/new fixture games use NULL, not an empty UUID/string.
3. Keep player identity, membership, and connection persistence exclusively in the generic lobby package.
4. Remove backend `Player` as active persistence; do not reintroduce it through a compatibility alias that continues writes.
5. Remove `AddPlayerToGame` and replace its obsolete max-plus-one tests with lobby explicit-selection coverage.
6. Keep game DAL independent from lobby membership lookup; binding goes through the explicit adapter service.
7. Preserve `GetGameByID` and its complete generation preload/not-found contract.
8. Keep `CreateGame(db)` for game fixtures without creating lobby membership state.
9. Remove the hardcoded unique `test-game` value so several games can exist in one fixture database.
10. Update remaining obsolete references; do not alter unrelated game generation/movement behavior.
11. Leave Castle.TeamColor, SoldierGroup.TeamColor, Map.PlayerAmount, and map-template data unchanged.

## Binding algorithm and endpoint

12. Use the exact operationId, response, statuses, and host requirement in the shared Island Battles binding contract; body carries game_key/lobby_id, identity comes from verified bearer.
13. Reuse the lobby authenticator; never trust a body player ID or accept the former placeholder `0` as authority.
14. Normalize deadlines and require current active host membership through the frozen lobby application boundary.
15. Reject scope/game mismatch and outsiders without disclosing room state; waiting/closed rooms cannot bind.
16. Accept only `island-battles`; `quiz-demo` proves generic reuse but must not create Island Battles rows.
17. Read the immutable start roster and ordered generic teams; never derive game colors from a later mutable live roster.
18. Persist mappings for all configured teams, including currently empty teams, ordered by immutable team order.
19. Map order 1..N to existing colors 1..N; reserve zero for neutral castles/soldiers.
20. Define island_game_team_bindings exactly as the shared contract specifies, containing game ID, generic team UUID, frozen team order, and numeric color.
21. Enforce unique `(game_id, generic_team_id)` and `(game_id, color)` so retries cannot create inconsistent duplicate mappings.
22. In one backend transaction, create a game with unique lobby link and its complete team mapping.
23. Concurrent bind requests rely on unique lobby linkage; resolve conflict by loading the committed existing game/mapping.
24. Do not swallow unrelated unique/FK/serialization errors as successful idempotent replay.
25. Both initial success and replay return the same game ID and stable mapping; response never exposes credentials/leases.
26. Authorize every replay before returning an existing game; a known lobby/game ID is not an access credential.
27. Access may expire after authorization completes; follow the explicit already-authorized binding-operation allowance and immutable roster contract.
28. Use a shared authenticated frozen-roster result rather than importing GORM internals from the generic service.
29. A mapping write failure must roll back new game creation; a game without a complete map is not a successful binding.
30. Do not create a map/generation, invoke Lambda, publish gameplay messages, or start a simulation here.
31. Keep the adapter dependency direction `backend/lobbyadapter` → `lobby`; generic packages never import backend.
32. Define public Huma summary/description explaining explicit binding, required started state, and idempotence.
33. Keep this game-specific endpoint out of the task 14 lobby-only SDK export.
34. Give task 13 the exact registration function, schema initialization order, authenticator dependency, and error mapping.

## Required tests and exact assertions

- `TestCreateMultipleGames`: create twice in one DB; distinct nonzero IDs and NULL lobby links.
- `TestGetGamePreservesGenerationPreloads`: existing graph fixture still loads generation maps/castles/commands/soldier groups.
- `TestBindStartedLobbyCreatesStableTeamMap`: teams ordered 1..N persist matching colors, including empty teams, with zero unused.
- `TestBindLobbyIsIdempotent`: sequential repeats return one game ID and unchanged mapping row count/content.
- `TestBindLobbyConcurrentRequests`: independent connections race; exactly one linked game and one map row per team remain.
- `TestBindLobbyRequiresHost`: an active nonhost receives 403, no game/map is created; creator host can bind.
- `TestBindLobbyRequiresAuthenticatedMember`: missing bearer, outsider, or forged player ID cannot bind or obtain an existing result.
- `TestBindLobbyRejectsWaitingAndClosed`: both phases fail with documented errors and zero game/map writes.
- `TestBindLobbyRejectsOtherGameKey`: started `quiz-demo` lobby functions generically but produces no Island Battles game.
- `TestBindLobbyUsesFrozenRoster`: member leaves/rejoins after start; game mapping remains identical to frozen team order.
- `TestBindReplayReauthorizesCaller`: leave before replay; replay fails authorization rather than returning game details.
- `TestBindMappingFailureRollsBackGame`: inject mapping insert failure; neither new game nor partial map persists.
- `TestBindDoesNotCreateGeneration`: successful binding creates game/map association rows only; generation count remains zero.
- `TestLobbyLifecycleDoesNotCreateGame`: create/join/start/heartbeat/reconnect authorization leave game count unchanged until explicit bind.

Use disposable PostgreSQL and actual generic service setup; generate identity-backed membership through legitimate test helpers.
For service fixtures, use external test packages where `testutils` imports would otherwise cause a cycle.
Race tests need independent database connections and barriers; assert durable counts, not only equal returned IDs.

## Verification and handoff

```sh
go test ./backend/lobbyadapter ./backend/dal ./backend/services -count=1
go test -race ./backend/lobbyadapter -count=1
go vet ./backend/lobbyadapter ./backend/dal ./backend/models
go list -deps ./lobby/...
```

Check the dependency output contains no backend package and list every removed caller requiring task 13 wiring.
Handoff endpoint registration/DTOs, mapping schema, commands/results, and unresolved integration checks.
Done requires stable concurrent binding, preserved simulation regressions, and a reusable generic lobby with no game imports.
