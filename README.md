# Searchlight CLI

A thin command-line client over the Searchlight MCP server. Same tools, same
auth, same payloads — but driven from the shell, optimized for agents and power
users.

> v0 audience: internal Headline users + portfolio CEOs already in the
> `is_internal?` allowlist on the eva-web server. External investors are
> blocked until that gate is relaxed; tracked as a follow-up Linear issue.

## Install

```bash
# macOS / Linux
brew install headlinevc/tap/searchlight

# Verify
searchlight version
```

Binaries for darwin/arm64, darwin/amd64, linux/amd64, and windows/amd64 are
published on each tag under the GitHub Releases tab.

## Quickstart

```bash
# 1. Sign in (opens browser, PKCE + loopback redirect)
searchlight auth login

# 2. Confirm identity
searchlight auth whoami --pretty

# 3. List available tools (124 today)
searchlight tools --pretty | jq '.tools | length'

# 4. Run any tool — by name, with --json
searchlight lookup_company --json '{"domain":"openai.com"}' --pretty

# 5. ...or with per-property flags
searchlight lookup_company --domain openai.com --pretty
```

## Authentication

The CLI uses OAuth 2.0 authorization_code + PKCE (S256) against the eva-web
MCP server, with a loopback redirect on an ephemeral port. Tokens are stored
in your OS keyring (macOS Keychain / Windows Credential Manager / libsecret),
falling back to a 0600 file at `$XDG_CONFIG_HOME/searchlight/credentials.json`
when no keyring is available (headless Linux, containers, CI).

| Command | Purpose |
|---|---|
| `searchlight auth login` | Browser-driven PKCE flow, persist tokens |
| `searchlight auth whoami` | Calls `get_current_user`, prints JSON |
| `searchlight auth refresh` | Force a refresh-token exchange |
| `searchlight auth logout` | Delete stored credentials |

## Output contract

Optimized for agents per the agent-CLI design principles documented in
[`docs/agents.md`](docs/agents.md):

- **stdout** is always JSON (the tool result envelope's first content block,
  pass-through verbatim).
- **stderr** is for human progress and hints; suppressed with `--quiet` or when
  stdout is not a TTY.
- Use `--pretty` to indent JSON for human reads.
- Exit codes: `0` success, `1` general failure, `2` usage, `3` not found,
  `4` permission denied, `5` conflict, `7` transport.

## Tool discovery

Tool definitions are fetched at runtime from the MCP server's `tools/list`
endpoint and cached under `$XDG_CACHE_HOME/searchlight/tools-<version>.json`
for 24h. The cache key embeds the server's reported version, so a server bump
invalidates the cache automatically.

| Command | Purpose |
|---|---|
| `searchlight tools` | List all tools (JSON) |
| `searchlight tools describe <name>` | Print one tool's schema |
| `searchlight tools refresh` | Force re-fetch from the server |
| `searchlight <tool-name> --help` | Per-tool help, parameter table, JSON example |

## Configuration

| Env var | Default | Purpose |
|---|---|---|
| `SEARCHLIGHT_URL` | `https://searchlight.io` | Server base URL — point at staging by setting this |
| `SEARCHLIGHT_CLIENT_ID` | (baked at build) | Override OAuth client_id |

## Mutating tools

Tools with side effects (prefixed `create_`, `update_`, `delete_`, `send_`,
`add_`, `remove_`, `ingest_`, `run_`, `parse_`, `cancel_`, `pause_`,
`unpause_`, `save_`, `report_`) support `--dry-run`, which prints the resolved
payload and exits without calling the server.

```bash
searchlight send_email --dry-run --json '{"to":"a@b.com","subject":"hi","body":"x"}'
```

## Development

```bash
# Tests
go test ./...

# Local build (placeholder client IDs)
CGO_ENABLED=0 go build \
  -ldflags="-X github.com/headlinevc/searchlight-cli/internal/config.prodClientID=$PROD_CID -X github.com/headlinevc/searchlight-cli/internal/config.stagingClientID=$STAGING_CID" \
  -o searchlight .

# Point at staging
SEARCHLIGHT_URL=https://staging.searchlight.io ./searchlight auth login
```

The two `client_id` values are minted by a one-time `OauthApplication.create!`
in the eva-web Rails console (production and staging) — see the
implementation plan and ask Diogo for the values.

## Related

- Eva-web MCP tool source: `lib/mcp/tools/*.rb` (124 tools as of server v4.2.0)
- Eva-web MCP transport: `app/controllers/api/mcp_controller.rb`
- Linear ticket: EVA-9938
