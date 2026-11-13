---
name: breaking-changes
description: Apply when changing the player API, the game API, the hooks, Centrifugo event payloads, stored lobby data or columns, or the Python client. The lobby breaks API compatibility rather than carrying code for old clients, but never resets or strands lobby data.
---

# Break API compatibility instead of carrying it; keep the data

Clean, compact code matters more than compatibility. When the player API, the game API, a hook or an event should change, change it directly and have clients and games upgrade. Old clients are not a reason to keep extra code.

## Data that must survive

Lobby data, every `lobby_*` table, is never reset: games share the lobby, so resetting or breaking its data would break all of them. Change the schema only through a new versioned migration in `postgres/migrations/` that keeps existing rows working, with a backfill where needed, and update the GORM record in the same change. Never edit an applied migration.

## Do

- Rename, retype or remove fields, endpoints and hook payload fields outright, in a single change.
- In the same change: export the API documents (`api/`), regenerate the Python models, update the Python wrapper and its CLI, the Go example in `clients/`, the lobby page, the mock game, the live suite and the docs.
- Say in the pull request description, and at the top of the [Python client's README](../../../clients/lobby-python/README.md), that the change is breaking and what clients must do. A change to the hooks or the game API is breaking for games: say what a game must change, since games pin the module and update on their own.

## Don't

- Add aliases, deprecated fields, fallback decoders, dual-read paths or version branches for older clients.
- Propose compatibility shims as follow-up fixes. Mention the break, and leave it.

Compatibility that costs no code is fine, such as generated clients ignoring unknown response fields. Add compatibility code only when the owner explicitly asks for it.
