---
name: lobby-development
description: Apply the lobby's conventions when adding or changing Huma endpoints, application services, GORM persistence and migrations, game hooks or the game API.
---

# Lobby development

## Game-neutral first

The lobby knows no game. Game settings, seat settings and member settings are opaque JSON objects that the lobby canonicalizes, stores and hands to the game's hooks; it never reads a field of them. A game's own rules reach the lobby only through a `domain.GameRegistration` field, a hook in `gamehooks`, or its frontend catalog entry. When a game needs more, add a generic field or hook that any game could use, document it in the README's "Binding a game over HTTP", implement it in the mock game (`mockgame/`) and test it there. Use a neutral key such as `example-game`, `quiz-demo` or `mock-game` for new tests and examples.

## Package boundaries

- `domain/` holds records, request types, errors, policies, pure state rules and the repository ports. It imports no adapter, Huma, GORM or gocent, and does no I/O.
- `application/` holds the commands and coordinates the domain contracts. Each command runs in one `Store.Transact` through `TransactNormalized`, whose callback makes no network calls: hooks that a command needs run before the transaction, except `ready-check`, which runs inside it and must answer quickly.
- `httpapi/` registers the player API's Huma operations in `routes.go`, keeps handlers in `handlers.go` and the DTOs in `dto.go`, and the game API in `game_api.go`. `postgres/` holds persistence; `realtime/` Centrifugo; `auth/` and `playertoken/` credentials and tokens; `gamehooks/` the HTTP side of games; `app/` the wiring from `LOBBY_*` configuration, which every command and an in-process host share.
- When an endpoint's public behavior changes, update its Huma `Summary` and `Description` to match, then export the API documents and regenerate the Python models (README, **Commands**). Never edit `api/*.json` or `clients/lobby-python/lobby_api_client/` by hand.

## Persistence and migrations

- Use GORM model structs for query filters and `clause` expressions for ordering, never handwritten condition strings such as `"lobby_id = ?"`.
- Prefer `Limit(1).Find(&slice)` for optional single-row lookups, and check the error and the slice's length before using a result.
- The schema is versioned SQL in `postgres/migrations/`. Add the next `NNNN_description.sql`, numbered without gaps, and update the GORM record in `postgres/models.go` in the same change; a test fails when records and columns differ. Never edit an applied migration. Lobby data is never reset: follow the [breaking-changes skill](../breaking-changes/SKILL.md).

## Tests

- Database tests use `internal/testutil`'s disposable PostgreSQL containers (Testcontainers); they need Docker or Podman, not a configured database.
- A change to how the lobby calls games gets a test against the mock game over real HTTP servers (`mockgame/integration_test.go`), and, when it changes what players see, a check in the live suite (`integration/`, run with `./scripts/test-acceptance.sh --mock-game`).
- A change to the lobby page (`frontend/`) gets a test in the matching `*.test.mjs`, run with `node --test frontend/*.test.mjs`.
- Tests must not depend on the machine they run on (CI runners have two cores). Never skip, delete or weaken a test to make it pass.
- Use descriptive variable names, such as `membership` rather than `m`.
