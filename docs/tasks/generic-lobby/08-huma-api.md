# 08 — Reusable public Huma API

Prerequisite: task 01 for DTO/route development; 03 and 05–07 for integration. Read existing `backend/routes/routes.go`, handlers with `Body` inputs, and the backend-development skill. Register routes inside reusable `lobby/httpapi`; backend/routes later makes a thin registration call.

## Exclusive ownership

Create `lobby/httpapi/dto.go`, `handlers.go`, `routes.go`, `errors.go`, `auth.go`, `schemas.go` and tests. No backend wiring, Centrifugo proxy handlers, generator output, GORM model exposure, or changes to other task files. Task 14 must be able to call your registration without a database or environment configuration.

## Steps

1. Implement every exact public path and operationId from contracts, using named typed inputs/responses. Set method, status, tag `Lobby`, Summary and Description. Ensure body fields really appear in Huma JSON input schemas; the old ConnectInput is not a template.
2. Build handlers around injected Application/Authenticator/ConnectionTokenIssuer ports. Constructor/route registration must not dereference live dependencies, open network connections, or start workers. This allows offline registration with fakes and schema export.
3. Register `LobbyBearer` HTTP bearer security. `/sessions` explicitly permits anonymous creation; all other public operations require verified bearer. Describe guest credentials separately from Centrifugo JWTs. Enforce real bearer middleware before Huma body/path validation (security schema metadata is documentation only). Authentication must run before sensitive state lookup, including invalid guest attempts with claimed player IDs.
4. Add schema constraints for UUIDs, names, registered key syntax, team counts/capacities, request_id, and positive generation. Team selection input requires an explicitly present field, allowing JSON null; omitted is 422. Ready requires an explicitly present boolean; false must not be mistaken for omission.
5. Marshal Snapshot/HeartbeatResult with deterministic lists and snake_case names. Make nullability explicit. Dates use date-time. No GORM fields such as DeletedAt and no internal job/credential hashes are serialized.
6. Implement typed error → Huma problem mapping and code extension exactly as contracts. Preserve framework validation details where safe. Unknown errors return a generic message; log correlation/context through injected logging if present, never credentials or raw request headers.
7. Leave/release return empty 204 without Body. Create returns 201 for initial and identical retry. Map missing room/wrong scope/outsider to indistinguishable not_found responses. Member-not-host is 403; failed capacity/phase/readiness is 409.
8. Explicitly register `LobbyInvalidation` event component with the Huma schema registry, even though no operation returns it. Its only fields are the frozen invalidation fields; do not introduce arbitrary payload. Task 14 exports this component and task 15 must generate its model.
9. Keep proxy routes out of this registration. Exporter must include only player routes/components and preserve security, never `/internal/...`, map/generation routes, graph HTML, or ping RPC.
10. Test with humatest/httptest and deterministic fakes for routing/auth. Add a small integrated create→join→select test using real Application once dependencies finish. Do not use handler tests as a substitute for database concurrency tests.

## Required tests

| Test | Arrange and assert |
| --- | --- |
| `TestPublicLobbyOperationContract` | Enumerate all 11 routes; exact operation IDs, methods, statuses and bearer security |
| `TestAnonymousSessionOnly` | Session creation works; all other endpoints reject missing/bad bearer with 401 |
| `TestAuthenticationPrecedesInputValidation` | Malformed body/path parameters with missing/invalid bearer produce 401, not 422, and never reach service |
| `TestClaimedPlayerCannotOverrideBearer` | Principal delivered to fake service is bearer owner despite another ID in input |
| `TestTeamNullVersusMissing` | null clears team; absent field is 422; valid UUID reaches service; malformed UUID is 422 |
| `TestReadyFalseVersusMissing` | false is accepted/preserved; absent body field is 422 |
| `TestProblemStatusAndCodeMapping` | Each domain error maps to expected status/code; unknown/DB errors expose no raw SQL/secret |
| `TestSnapshotDoesNotExposeInternalFields` | JSON contains only documented fields; arrays empty rather than null and large int64 version preserved |
| `TestDeleteResponsesAreEmpty` | Both delete operations have zero response body with 204 |
| `TestRegistrationIsOffline` | Construct/register using inert dependencies with no DB/secret/network and inspect all schemas |
| `TestInvalidationSchemaRegistered` | Named component exists with all required fields and no arbitrary data field |
| `TestCreateJoinTeamHTTPFlow` | Two authenticated guests use routes against real service; roster/team selection and authorization match persisted state |

## Verification and handoff

Run `go test ./lobby/httpapi` and inspect generated Huma JSON in tests. Use a contract assertion rather than a huge fragile golden of unrelated schemas. Hand task 14 the exact registration function/config needed for offline export, and task 13 the production dependency constructor. Update descriptions whenever public semantics change.
