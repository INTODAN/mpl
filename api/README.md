# Generic lobby API artifacts

`openapi.json` is the player-facing OpenAPI 3.1 contract for the 11 routes
registered by `lobby/httpapi`. `events.json` is the standalone JSON Schema for
the `LobbyInvalidation` event delivered through realtime transport.
`game-api.openapi.json` is the OpenAPI contract of the game API, which games
that the lobby calls over HTTP use to read the lobby
(`httpapi.RegisterGameAPI`; see "Binding a game over HTTP" in the lobby
README). It is a document of its own, so the player SDKs never include it.
All three files are generated offline from the Go/Huma DTOs and registration;
those DTOs are the source of truth.

Regenerate and verify the checked-in artifacts from the lobby component root (`lobby/` in the monorepo):

```sh
go run ./cmd/export-lobby-openapi \
  --output api/openapi.json \
  --events-output api/events.json \
  --game-api-output api/game-api.openapi.json
go run ./cmd/export-lobby-openapi \
  --output api/openapi.json \
  --events-output api/events.json \
  --game-api-output api/game-api.openapi.json \
  --check
```

With no `--output`, the OpenAPI JSON is written to stdout. `--check` compares
the explicitly supplied paths and does not modify them.
