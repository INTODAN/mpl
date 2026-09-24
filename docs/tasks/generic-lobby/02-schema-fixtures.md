# Task 02 — Lobby PostgreSQL schema and independent fixtures

Status: implemented; see [verification evidence](implementation-status.md). Prerequisite: task 01's Go contracts are frozen and compile.
Read [contracts](contracts.md), [architecture](architecture.md), and [task order](README.md) first.
Read `backend/models/game.go`, `backend/dependencies/database.go`, `backend/internal/testutils/test_database.go`, and `go.mod`.
Apply `.agents/skills/backend-development/SKILL.md`; its struct/clause query conventions apply here.
Use the existing PostgreSQL/GORM/testcontainers dependencies; do not add another Go module.

## Exclusive ownership

- `lobby/postgres/models.go`, `lobby/postgres/schema.go`, `lobby/postgres/schema_test.go`.
- `lobby/internal/testutil/postgres.go` and fixture-specific tests in that directory.
- No root domain types, backend wiring, identity/store implementation, or unrelated fixtures.
- Ask task 01 to change contracts; ask task 13 to call the schema initializer during bootstrap.

## Deliverable

Provide lobby-owned GORM records with explicit table names and an error-returning schema initializer.
Provide a disposable PostgreSQL fixture usable by every package below `lobby/`.
Importing these packages must not connect, migrate, inspect environment, launch workers, or exit.
The schema must support tasks 03/04/07/10 without parallel agents editing `models.go`.
Confirm all record fields with those owners before declaring this prerequisite complete.

## Implementation steps

1. Translate frozen domain records into persistence records; keep persistence types out of domain ports.
2. Explicitly name `lobby_players`, `lobby_sessions`, `lobby_rooms`, `lobby_teams`, and `lobby_memberships`.
3. Also name `lobby_leases`, `lobby_start_roster`, `lobby_create_requests`, and `lobby_outbox`.
4. Use UUID primary keys or the frozen stable composite keys; versions/generations use positive bigint values.
5. Avoid embedded `gorm.Model` soft deletion on active tables; membership uses explicit active/generation fields.
6. Preserve nullable host/team/deadline/release fields; absence must remain SQL NULL rather than an invented zero UUID.
7. Sessions store only credential hashes, player linkage, and expiry; raw bearers have no database column; enforce unique session player_id.
8. Persist positive room next_join_sequence for monotonic joining order. Add unique `(game_key, join_code)` to rooms; retain codes for closed rooms.
9. Add unique `(lobby_id, player_id)` to memberships, retaining inactive history in the same record.
10. Add unique `(lobby_id, team_order)` and `(lobby_id, normalized_name)` to teams.
11. Persist the normalized team name used by the frozen domain helper; do not invent database-specific case folding.
12. Add composite team identity `(lobby_id, id)` and composite membership team FK to reject cross-room teams.
13. Add unique lease `(lobby_id, player_id, generation, client_instance_id)` and positive generation constraints.
14. Preserve old-generation lease history; never force old leases to reference only the current membership generation.
15. Store immutable start roster with player/member/team/order values; constraints must not require members to remain active.
16. Create requests use unique `(player_id, request_id)`, canonical payload hash, and the original response representation.
17. Outbox contains typed invalidation/revocation jobs, event IDs, attempt count, next-attempt time, claim token/deadline, and acknowledgement state.
18. Enforce invalidation `(lobby_id, version)` uniqueness without preventing multiple revocations for that same version.
19. Use a partial unique index for invalidation jobs if sharing the outbox table; freeze discriminator values with task 04.
20. Add CHECK constraints for room/team capacity bounds, valid phases, positive versions/generations, and valid team order.
21. Cross-row totals, team count, live capacity, and host eligibility remain transaction/domain checks, not misleading CHECK expressions.
22. Index session expiry, lease deadline lookup, scope lookup, and pending/expired outbox claims.
23. Build the schema in dependency order; add cyclic/composite FKs after prerequisite tables exist when necessary.
24. Make repeated schema initialization safe; return errors instead of `log.Fatal` or panic.
25. The initializer creates the complete fresh generic schema.
26. Fixture starts disposable `postgres:18-alpine`, opens GORM, initializes this schema, and registers container/pool cleanup.
27. Do not import `backend/internal/testutils`: Go internal visibility forbids that dependency from root `lobby/`.
28. Return `*gorm.DB`; permit independent connections and explicit transactions for subsequent race tests.
29. Use a realistic container startup timeout and report Docker startup failures as failures, not skipped passes.

## Required tests and exact assertions

- `TestSchemaCreatesOnlyLobbyTables`: all nine named tables exist; no `games`, `players`, or simulation tables are created.
- `TestSchemaInitializationIsRepeatable`: initialize twice around a seeded room; seed values/count remain unchanged.
- `TestSchemaRejectsDuplicateMembership`: second row for one room/player fails while that player in another room succeeds.
- `TestSchemaAllowsInactiveMembershipReuse`: deactivate/reactivate the existing row without deleting it; row ID is stable.
- `TestSchemaRejectsCrossLobbyTeam`: membership in A with team from B fails at the database FK layer.
- `TestSchemaRetainsOldGenerationLeases`: increment member generation while preserving previous lease rows; update succeeds.
- `TestSchemaEnforcesLocalBounds`: table-driven invalid zero version, zero generation, capacity 1/65, and invalid phase are rejected.
- `TestSchemaScopesJoinCodeUniqueness`: duplicate code in one game fails; same code in another game succeeds; closed code remains reserved.
- `TestSchemaAllowsOneSessionPerGuest`: a second session for the same player fails; different players have independent sessions.
- `TestSchemaScopesTeamUniqueness`: duplicate order/normalized name within a room fails; another room can reuse both.
- `TestSchemaSeparatesInvalidationAndRevocationUniqueness`: duplicate invalidation version fails; two member revocations at that version succeed.
- `TestSchemaPreservesNullAndFalse`: unassigned/inactive/not-ready rows read back with SQL NULL and false intact.
- `TestFixtureIsolation`: two fixture databases do not share a seeded room; no developer database URL is used.

Use real PostgreSQL for constraints; do not substitute SQLite. Test schema behavior, not every field assignment.
Use raw fixed DDL only where schema creation requires it; query filters/orders follow struct/clause conventions.

## Verification and handoff

```sh
go test ./lobby/postgres -run '^TestSchema' -count=1
go test ./lobby/internal/testutil/... -count=1
go vet ./lobby/postgres ./lobby/internal/testutil/...
go list -deps ./lobby/...
```

The dependency output must contain no `islandBattles/backend/` imports.
List tables, record names, exported initializer/fixture signatures, and indexes for tasks 03/04.
Record exact test results and Docker limitations; a blocked container run is not completion evidence.
Done means schema and fixture tests pass and all downstream owners agree on the shared persistence fields.
