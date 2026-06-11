# Agent invariants for searchlight CLI

Read this before driving the CLI from an agent. Short by design — these are
the load-bearing facts; everything else is in `searchlight --help`.

## Discovery

- `searchlight tools` returns a JSON object `{ count, tools: [{name, description, inputSchema}, ...] }`. Use this once at the start of a session.
- `searchlight tools describe <name>` returns one tool's full schema.
- The cached schema is at `$XDG_CACHE_HOME/searchlight/tools-<server-version>.json` with a 1-hour TTL. It refreshes itself at two moments: right after a successful `auth login`, and at startup when you invoke a dynamic tool command with a stale cache and stored credentials (bounded to ~3s). A failed startup refresh silently falls back to the cached schema when one exists; with no cache to fall back to, or when `--no-cache` explicitly demanded freshness, the failure surfaces with its normal exit code (e.g. 7 for transport). You normally never need `searchlight tools refresh` — it remains as a manual escape hatch, as does `--no-cache` on any command.
- Static commands (`help`, `version`, `auth *`, `tools *`, `completion`, bare/flag-only invocations) never trigger a startup fetch — they work offline and pre-auth.

## Invocation

- **Prefer `--json '<payload>'`** over per-property flags. It maps 1:1 to the underlying API and avoids string-coercion surprises.
- Per-property flags exist as convenience for humans (e.g. `--domain openai.com`); both forms produce identical payloads.
- Required fields can be satisfied by either form; the CLI validates at runtime, not via flag flags.

## Output

- **stdout is always JSON.** Parse it. The result envelope already maps to the underlying tool's response shape.
- **stderr is for humans.** Spinners, hints, login prompts. You can ignore it or suppress with `--quiet`.
- `--pretty` adds indentation. Skip it unless a human is reading.

## Exit codes

| Code | Meaning | Retry? |
|---|---|---|
| 0 | Success | n/a |
| 1 | General failure (tool returned `isError: true`) | Maybe — inspect message |
| 2 | Usage error (bad flags) | No — fix the command |
| 3 | Not found | No — fix the input |
| 4 | Permission denied | Try `searchlight auth refresh`, then once-more |
| 5 | Conflict (already exists) | Treat as idempotent success in most flows |
| 7 | Transport (network) | Yes, with backoff |

## Mutating tools

Mutators are tools whose names start with `create_`, `update_`, `delete_`, `send_`, `add_`, `remove_`, `ingest_`, `run_`, `parse_`, `cancel_`, `pause_`, `unpause_`, `save_`, `report_`.

- Use `--dry-run` first when uncertain about a payload. It prints the resolved JSON and exits 0 without calling the server.
- No interactive prompts. The CLI fails fast on missing required fields rather than asking.

## Authentication

- If a tool call returns exit 4 (permission denied), try `searchlight auth refresh` once. If that still fails, ask a human to run `searchlight auth login`.
- The CLI auto-refreshes access tokens transparently using the stored refresh token; you don't need to manage token lifecycle.

## Server pointing

- `SEARCHLIGHT_URL` env var selects the target server (default `https://searchlight.headline.com`).
- The OAuth `client_id` is selected automatically based on the server URL.

## Things you should NOT do

- Do not parse stderr for results — it's prose, not contract.
- Do not assume tools are stable across server versions — re-read `tools/list` after a CLI upgrade or schema-cache refresh.
- Do not pipe the human-formatted output of one command into JSON parsing in another. Use `--pretty` or not, the stdout is always valid JSON either way.
