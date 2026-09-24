# Task 14 — Export the lobby OpenAPI contract without infrastructure

Status: implemented; see [verification evidence](implementation-status.md). Prerequisite: task 08 public route registration and DTOs are complete.
Read [README](README.md), [architecture](architecture.md), and [contracts](contracts.md).
The HTTP table and `Invalidation` shape are normative; this task does not design another API.

## Exclusive ownership

- `lobby/cmd/export-lobby-openapi/main.go` and `main_test.go`.
- `lobby/api/openapi.json`, `lobby/api/events.json`, and `lobby/api/README.md`.
- No edits to `lobby/httpapi/`, backend bootstrap, Python packaging, or generated Python.
- Ask task 08's owner to correct missing DTOs, operation IDs, or reusable registration hooks.

## Outcome and implementation sequence

1. Implement an ordinary Go executable in the existing module; do not create another module.
2. Construct a Chi router and Huma API using task 08's public registration/configuration functions.
3. Register all 11 public operations from the contract, with no internal proxy or gameplay routes.
4. Inject inert dependencies solely to construct handler bindings; never invoke handlers while exporting.
5. Do not import `backend/dependencies`, open PostgreSQL, contact Centrifugo, or start goroutines.
6. Do not require deployment environment variables or signing/proxy secrets.
7. Use the pinned Huma v2.34.1 `api.OpenAPI().MarshalJSON()` API for OpenAPI 3.1 output.
8. Serialize stable UTF-8 JSON with two-space indentation and exactly one trailing newline.
9. Keep title/version stable: `Generic Lobby API` and `1.0.0`; no timestamps or machine paths.
10. Support `--output PATH`; omitted output writes only the OpenAPI JSON to stdout.
11. Support `--events-output PATH` for a standalone JSON Schema artifact for `Invalidation`.
12. Both artifacts must derive from the same registered Go schema, never duplicate its fields manually.
13. Ask task 08 to register a named `LobbyInvalidation` component if registration currently omits it.
14. Keep that component in OpenAPI even though HTTP operations do not return publications.
15. Resolve the standalone event schema's referenced components into local `$defs` when necessary.
16. Make every standalone schema reference resolvable from `events.json` without network access.
17. Support `--check` with explicit output paths: compare expected bytes without writing files.
18. Missing or different artifacts make check mode fail with a concise stderr diagnostic.
19. Validate flag combinations before writing; report invalid paths and serialization errors nonzero.
20. Write through temporary files and rename so a failed write cannot truncate a valid artifact.
21. Document commands and identify Go/Huma DTOs as the source of truth in `lobby/api/README.md`.

## Contract checks before handing off

- Prefix every public route with `/lobby/v1` and preserve exact path/query parameter names.
- Require explicit, unique operation IDs matching the HTTP table, including lease release.
- Declare `LobbyBearer` on protected operations; session creation remains unauthenticated.
- Schema security metadata must not accidentally expose proxy-secret headers to player clients.
- Include named snapshot, request, heartbeat, invalidation, and problem-response schemas.
- Preserve integer/int64 versions and membership generations; arrays are arrays, not nullable arrays.
- Preserve explicitly required nullable `team_id`: null clears assignment; omission is invalid.
- Preserve required `ready` even when false; required query `generation` is an integer.
- Preserve 201 create/session responses and bodyless 204 leave/release responses.
- Include the stable problem `code` field and declared error statuses from task 08.
- Never claim the standalone event schema implements a WebSocket dispatcher.

## Required tests and exact assertions

Use Go tests in the owned executable directory; none need Docker or a running server.

- `TestExportDeterministic`: two independently constructed APIs yield byte-identical artifacts.
- `TestExportPublicOperations`: exactly the contract's 11 operation IDs appear once each.
- `TestExportExcludesInternalAndGameplay`: no `/internal/`, `/connect`, `/game/`, or map/generation route.
- `TestExportSecurity`: session creation is public and every remaining operation has `LobbyBearer`.
- `TestExportRequiredNullableAndFalse`: team is required+nullable; ready is required boolean.
- `TestExportResponses`: assert create/session 201, leave/release 204, and typed problem errors.
- `TestInvalidationComponent`: schema has version, scope, event ID, schema version, and fixed type.
- `TestEventSchemaSelfContained`: traverse every `$ref` and resolve it within the event artifact.
- `TestExportWithoutInfrastructure`: execute with absent deployment env; no bootstrap is needed.
- `TestCheckMode`: matching files pass; changed/missing files fail and remain untouched.
- `TestCheckedInArtifacts`: production exporter output equals both committed JSON artifacts.
- `TestExportWriteFailure`: an invalid destination returns nonzero and preserves existing artifacts.

## Verification commands

Run from repository root after task 08 lands:

```sh
go test ./lobby/cmd/export-lobby-openapi
go run ./lobby/cmd/export-lobby-openapi --output lobby/api/openapi.json --events-output lobby/api/events.json
go run ./lobby/cmd/export-lobby-openapi --output lobby/api/openapi.json --events-output lobby/api/events.json --check
```

After intentional schema changes, regenerate before running the checked-in-artifact assertion.
Do not start the backend merely to download `/openapi.json`; export must work offline.

## Completion evidence

Report changed files, test results, both artifact SHA-256 hashes, and successful check-mode output.
List missing upstream contract details explicitly; do not mark the task complete using fake schemas.
Hand task 15 the operation-ID list, component names, and schema artifact paths.

## Verified reference

[Huma OpenAPI generation](https://huma.rocks/features/openapi-generation/) documents deriving schemas
from registered operations; the repository pins Huma v2.34.1, whose local source exposes `MarshalJSON`.
