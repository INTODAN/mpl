# Task 15 — Generate and lock the Python HTTP SDK

Status: implemented; see [verification evidence](implementation-status.md). Prerequisite: task 14 exports the final public lobby schema.
Read [README](README.md), [architecture](architecture.md), and [contracts](contracts.md).
Generate commands/models from Huma; do not implement lobby behavior in generated files.

## Exclusive ownership

- `lobby/clients/lobby-python/pyproject.toml`, `uv.lock`, and `.gitignore`.
- `lobby/clients/lobby-python/tools/pyproject.toml` and `tools/uv.lock` for isolated generation tools.
- `lobby/clients/lobby-python/generator-config.yaml` and `scripts/generate.py`.
- Entire `lobby/clients/lobby-python/generated/`, a complete generated installable project.
- `lobby/clients/lobby-python/tests/test_generated_http.py` and `tests/test_generation.py`.
- No handwritten `lobby_client/`, CLI/examples, user README, or `lobby/api/` edits.

## Packaging and dependency decisions

1. Keep the handwritten project Python requirement `>=3.14`, matching the existing scaffold.
2. Use a separate tools project so initial generation can run before `generated/` exists.
   Mark that dependency-only tools project `package = false` under `[tool.uv]`.
3. Pin tools dependency `openapi-python-client==0.29.0`; lock all its transitive dependencies.
4. Explicitly pin the selected Ruff version in the tools project; formatter changes alter output.
5. Pin runtime `centrifuge-python==0.6.0`; include `httpx`, pytest, and async test tooling as needed.
6. Lock runtime/development dependencies and the local generated project's resolved dependencies.
7. Configure a local editable dependency on `./generated`, whose import is `lobby_api_client`.
8. Set generator config `project_name_override: lobby-api-client` and `package_name_override: lobby_api_client`.
9. Set `package_version_override: 1.0.0`; use deterministic, locked formatting hooks.
10. Select `--meta uv` so `generated/` contains its own installable metadata and package sources. This option does not emit uv.lock; use the root/tools locks and do not require or hand-create a generated lockfile.
11. Keep root packaging restricted to handwritten `lobby_client`, excluding tools/tests/examples/generated.
12. Do not copy the old project's broad Ruff configuration onto generated sources.
13. Leave any generated minimum-Python metadata as generator output; the wrapper requires 3.14.

## Deterministic generation script

- Resolve repository paths from the script location, not the caller's current working directory.
- Run the task 14 exporter into a temporary file and compare it with `lobby/api/openapi.json`.
- Fail on schema drift and direct the caller to task 14's regeneration command; do not rewrite its files.
- Invoke the installed executable with an argument list; never interpolate a shell command.
- The pinned CLI accepts this command, with absolute paths substituted by the script:

```sh
openapi-python-client generate --path /tmp/lobby-openapi.json --config /absolute/generator-config.yaml --meta uv --output-path /tmp/lobby-sdk --fail-on-warning
```

- Use a fresh temporary output directory; do not generate directly over handwritten files.
- Confirm all 11 expected operations and the named invalidation model were generated.
- Copy the complete resulting project to `generated/` only after generation/validation succeeds.
- Remove stale generated files on successful replacement; retain the previous tree on failure.
- Do not customize templates or patch generated code to hide unsupported schema warnings.
- `--check` generates identically but compares filenames and bytes without modifying tracked files.
- Ignore only non-source caches such as `__pycache__`; compare metadata and generated README too.
- A failed generator, warning, missing operation, or differing file returns a nonzero exit code.
- Record tool versions in script diagnostics; emit no timestamp/source-host path inside generated files.

## Required tests and exact assertions

Use `httpx.MockTransport` to exercise actual generated asynchronous operation functions.

- `test_all_operations_generated`: all 11 operation IDs have callable async detailed functions.
- `test_create_and_join_payloads`: exact method/path/body; UUIDs and bearer credential serialize correctly.
- `test_required_nullable_team`: `None` produces `"team_id": null`, not omission or string `"null"`.
- `test_false_ready_serialized`: `False` remains present in the request body.
- `test_release_generation_query`: correct client-instance path and integer generation query parameter.
- `test_snapshot_models`: empty lists, allowed nulls, timestamps, and int64 versions round-trip.
- `test_problem_response`: mocked 401/404/409/422/503 bodies retain application code and detail.
- `test_no_content`: both 204 operations succeed without attempting JSON parsing.
- `test_unknown_status`: unexpected HTTP status remains observable with status/body information.
- `test_invalidation_model`: generated parser handles the exact schema without a handwritten duplicate.
- `test_generation_check`: matching output passes; changed, extra, or missing files fail without mutation.
- `test_generation_repeatable`: two clean temporary outputs have identical paths and bytes.
- `test_failed_generation_preserves_output`: generator failure leaves the existing tree intact.

Generation tests invoke the script through the locked tools project, not the runtime environment's PATH.

## Verification commands

Run from repository root; create locks during implementation, then prove locked execution:

```sh
uv sync --project lobby/clients/lobby-python/tools --locked
uv run --project lobby/clients/lobby-python/tools --locked python lobby/clients/lobby-python/scripts/generate.py
uv sync --project lobby/clients/lobby-python --locked --no-install-project
uv run --project lobby/clients/lobby-python --locked --no-install-project pytest lobby/clients/lobby-python/tests/test_generated_http.py lobby/clients/lobby-python/tests/test_generation.py
uv run --project lobby/clients/lobby-python/tools --locked python lobby/clients/lobby-python/scripts/generate.py --check
```

`--no-install-project` installs the generated dependency while task 16's handwritten package is absent.
After task 16 lands, normal locked sync also installs the wrapper; this avoids a circular task dependency.
Generation must work without live PostgreSQL, Centrifugo, backend, or external schema retrieval.
First-time dependency installation may need network; a cached locked regeneration must not need services.

## Completion evidence and references

Report locked versions, generated operation/model import names, test results, and clean regeneration check.
Hand task 16 an actual installed async-call example plus generated snapshot/invalidation import paths.
Do not claim Python 3.14 compatibility until install/import and generated HTTP tests run on Python 3.14.
[Pinned generator CLI](https://raw.githubusercontent.com/openapi-generators/openapi-python-client/v0.29.0/openapi_python_client/cli.py)
defines `--fail-on-warning`; [generator documentation](https://github.com/openapi-generators/openapi-python-client/tree/v0.29.0)
documents local-schema generation and package-name overrides. No tool was installed while planning this task.
