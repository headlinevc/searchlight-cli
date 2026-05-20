# Searchlight CLI — Project Contract

A thin Go client over the Searchlight MCP server (eva-web). The tool surface is
fully derived at runtime from the MCP server's `tools/list`, so adding new
tools server-side requires zero changes here. Audience is internal Headline
users + portfolio CEOs (today) and sophisticated external investors (after the
`is_internal?` gate is relaxed server-side).

## What this CLI is for

- Give power users a fast, agent-friendly way to drive Searchlight, analogous
  to how the Linear CLI outpaces the Linear MCP for batch/automation work.
- Be a first-class target for AI agents (Claude Code, custom orchestrators):
  JSON output by default, deterministic exit codes, no interactive prompts,
  payload-oriented `--json` invocation that maps 1:1 to the underlying API.
- Avoid duplicating the MCP tool surface — fetch schemas from `tools/list`
  and dynamically register one cobra subcommand per cached tool.

Linear ticket: [EVA-9938](https://linear.app/headline/issue/EVA-9938/create-a-searchlight-cli).

## Architecture

```
┌─────────────────────────┐         OAuth PKCE          ┌──────────────────────┐
│  searchlight CLI (Go)   │  ◄─────────────────────────► │  eva-web /oauth/*    │
│  ─────────────────────  │                              │  /.well-known/*      │
│  cobra root             │         JSON-RPC POST        │                      │
│   ├─ auth login/logout  │  ──────────────────────────► │  /mcp                │
│   ├─ tools list/refresh │                              │   tools/list         │
│   └─ <dynamic from      │                              │   tools/call         │
│       tools.json cache> │                              │                      │
└─────────────────────────┘                              └──────────────────────┘
        │
        ├─ keyring: tokens.v1 (Tokens struct as JSON blob)
        └─ ~/.cache/searchlight/tools-<server-version>.json (24h TTL)
```

### Repo layout

```
searchlight-cli/
├── main.go                   # entry point, wires build info, exits with CodedError code
├── cmd/                      # cobra tree
│   ├── root.go               # globals, --pretty/--quiet/--no-cache, Execute()
│   ├── auth.go               # auth login/logout/whoami/refresh
│   ├── tools.go              # tools list/describe/refresh + writeToolResult
│   ├── dynamic.go            # registers one cobra subcommand per cached tool
│   ├── version.go            # cli + server version + schema cache age
│   └── keyring.go            # backend factory bound to globals.Cfg
├── internal/
│   ├── config/               # SEARCHLIGHT_URL, client_id selection, paths
│   ├── oauth/
│   │   ├── pkce.go           # 32-byte verifier → S256 challenge (43 chars)
│   │   ├── flow.go           # /.well-known discovery + browser loopback
│   │   ├── token.go          # Manager: refresh-on-expiry with clock skew
│   │   └── store.go          # KeyringStore (atomic JSON blob)
│   ├── keyring/              # OS keyring (zalando) + 0600 file fallback
│   ├── mcp/
│   │   ├── client.go         # JSON-RPC client, retry-once on 401
│   │   └── schema.go         # tools/list cache, version-keyed TTL
│   ├── output/               # JSON to stdout, human to stderr
│   └── errors/               # CodedError + exit code constants
├── docs/agents.md            # SKILL.md-style invariants for AI agents
├── .goreleaser.yaml          # darwin arm64/amd64 + linux + windows + brew tap
├── .github/workflows/
│   ├── ci.yml                # vet, test (-race), golangci-lint, gosec, smoke build
│   └── release-please.yml    # release-please → tag → goreleaser
├── release-please-config.json
├── .release-please-manifest.json
├── .golangci.yml
└── Makefile
```

### Dynamic command construction (load-bearing design)

Every invocation:

1. `main.go` calls `cmd.Execute()` which calls `setupGlobals()` → loads config,
   wires the OAuth manager and MCP client, sets the schema cache path.
2. If the schema cache file exists, `registerDynamicTools` reads it (no network)
   and adds one cobra subcommand per tool. Each gets:
   - `--json '<payload>'` (preferred for agents)
   - One `--<prop-name>` per `inputSchema.properties` entry
   - `--dry-run` if the tool name matches a mutator prefix (`create_`, `send_`,
     `delete_`, `update_`, `add_`, `remove_`, `ingest_`, `run_`, `parse_`,
     `cancel_`, `pause_`, `unpause_`, `save_`, `report_`)
3. If the cache is missing, top-level help still works; the user runs
   `searchlight auth login` then `searchlight tools refresh` to populate it.
4. Required fields are validated at runtime in `buildPayload` because they can
   be satisfied by either `--json` or per-property flags (cobra has no any-of
   semantics).

### Auth flow

PKCE + browser loopback against eva-web's custom OAuth implementation. All the
relevant server endpoints already exist (`POST /oauth/token`,
`/.well-known/oauth-authorization-server`, `OauthApplication` with wildcard
redirect URI support, PKCE S256 enforced in
`app/services/mcp/oauth/authorization_service.rb`).

The CLI:

1. Discovers endpoints via `/.well-known/oauth-authorization-server` (RFC 8414).
2. Generates a 32-byte verifier → 43-char S256 challenge (matches the server's
   exact-length validation).
3. Binds an ephemeral listener on `127.0.0.1:0`, opens browser to `/authorize`
   with `redirect_uri=http://127.0.0.1:<port>/callback`.
4. User logs in via Auth0, browser redirects to loopback, CLI captures `code`
   + validates `state`.
5. Exchanges code at `/oauth/token` → access (15min) + refresh (7-day) tokens.
6. Stores tokens in the OS keyring (zalando/go-keyring); falls back to
   `$XDG_CONFIG_HOME/searchlight/credentials.json` mode 0600 if no keyring.
7. The MCP client transparently refreshes on 401 via `oauth.Manager.ForceRefresh`.

### Output contract

- **stdout = JSON, always.** `writeToolResult` passes through `content[0].text`
  verbatim if it's valid JSON, otherwise wraps as `{"text": "..."}`.
- **stderr = human progress.** `output.HumanF(quiet, ...)` is no-op when
  `--quiet` is set. Use this for login prompts, "refreshed N tools", spinners.
- **Exit codes** are deterministic and documented in `docs/agents.md`:
  `0` success, `1` general failure, `2` usage, `3` not found, `4` permission,
  `5` conflict, `7` transport.

## Build & Test Commands

```bash
# Standard dev loop
make test           # go test -race -cover ./...
make lint           # go vet + golangci-lint
make build          # CGO_ENABLED=0 build with placeholder client IDs

# Coverage
go test -coverprofile=/tmp/cov.out -coverpkg=./... ./...
go tool cover -func=/tmp/cov.out | tail -1
# Aggregate target: ≥70% (currently 72.5%)

# Single test
go test -run TestClient_Call_401WithRefresh_Succeeds ./internal/mcp/...

# Reproduce the CI smoke build
CGO_ENABLED=0 go build \
  -ldflags="-X github.com/headlinevc/searchlight-cli/internal/config.prodClientID=ci-placeholder \
            -X github.com/headlinevc/searchlight-cli/internal/config.stagingClientID=ci-placeholder" \
  -o /tmp/searchlight . && /tmp/searchlight --help
```

## Coding Conventions

- **Go formatting**: standard `gofmt`. CI verifies `go.mod` is tidy via
  `go mod tidy && git diff --exit-code go.mod go.sum`.
- **Imports**: stdlib first, then third-party, then this module's internal
  packages — separated by blank lines.
- **Errors**: return `*internal/errors.CodedError` at boundaries so the cobra
  exit code is deterministic. Use `sterr.Wrap(code, msg, exit, err)` for
  wrapping, `sterr.New(...)` for synthetic errors. Plain `fmt.Errorf` is fine
  for internal/test code where exit code doesn't matter.
- **Comments**: explain WHY in domain terms, not WHAT. Never reference Linear
  ticket numbers in source (they go in commit messages, PR titles, branch names
  only — same rule as eva-web).
- **No `panic`** in CLI paths. Return errors. Tests can `t.Fatalf` freely.
- **HTTP clients**: always set a timeout. The MCP client uses 60s, OAuth
  endpoints use 5min for the browser-loopback window.
- **Response bodies**: always drain and close. The `bodyclose` linter enforces.
  The pattern in `internal/mcp/client.go`'s `Call` is the canonical example:
  an `attempt` closure that opens, drains, closes the body, and returns
  `(status, body, err)`.

## Testing Philosophy

Inherited from the eva-web project — applies here verbatim:

- **Never re-implement logic in tests.** Hardcode expected values from
  manually-verified examples. If the production code computes
  `2 * x + 5`, the test should assert `result == 11` for input `3`, not
  `result == 2*x + 5`.
- **Test the interface contract**, not internal mechanics. Design tests from
  the function signature and docstring, not from reading the body.
- **Integration over unit when sensible.** HTTP-touching code uses
  `httptest.NewServer`. Filesystem code uses `t.TempDir()`. We don't mock
  what isn't slow or non-deterministic.
- **No tests that re-implement Anthropic API logic in WebMock-style stubs.**
  Tests against the MCP server use simple JSON-RPC handlers that respond with
  the fields the test cares about, not a full simulation.
- **Coverage target: ≥70% aggregate.** Per-package floors:
  `errors` 100%, `output` 90%+, `mcp` and `config` 80%+, `cmd` and `oauth`
  60%+, `keyring` 60%+ (OS-keyring path is intentionally untested in CI).

## OAuth client setup (one-time, manual)

The CLI is a public OAuth client (no client_secret; PKCE proves possession).
Two `OauthApplication` rows must exist in eva-web — one in production, one in
staging. Run this in the **eva-web** Rails console for each environment:

```ruby
OauthApplication.create!(
  name: "Searchlight CLI",
  client_id: SecureRandom.uuid,                  # record this → GitHub secret
  client_secret: nil,
  redirect_uris: ["http://localhost:*", "http://127.0.0.1:*"],
  grant_types: %w[authorization_code refresh_token],
  response_types: %w[code],
)
```

The wildcard redirect URI support is already implemented in
`app/models/oauth_application.rb:13-21`. Record both `client_id`s and stash
them as GitHub secrets in this repo:

- `SEARCHLIGHT_PROD_CLIENT_ID`
- `SEARCHLIGHT_STAGING_CLIENT_ID`

The GoReleaser pipeline bakes them into the released binaries via
`-ldflags="-X github.com/headlinevc/searchlight-cli/internal/config.prodClientID=..."`.

## Release Process

Releases use **release-please** + GoReleaser. Flow:

1. Every push to `main` runs `.github/workflows/release-please.yml`.
2. `release-please-action` opens (or updates) a "release PR" titled
   `chore(main): release X.Y.Z`. The PR body contains an auto-generated
   `CHANGELOG.md` diff grouped by conventional-commit type
   (Features / Bug Fixes / Performance / Refactors / Dependencies / Docs).
3. When the release PR is merged, release-please creates the git tag
   (`vX.Y.Z`) and a GitHub Release.
4. The same workflow then chains into a `goreleaser` job that builds the
   cross-platform binaries, pushes them to the Release as assets, and
   updates the `headlinevc/homebrew-tap` Formula.

### Conventional commits

Required for release-please to compute version bumps. Format:

```
<type>(<scope>): <subject>
```

| Type | Bump | Changelog section |
|---|---|---|
| `feat:` | minor | Features |
| `fix:` | patch | Bug Fixes |
| `perf:` | patch | Performance Improvements |
| `refactor:` | patch | Code Refactoring |
| `deps:` | patch | Dependencies |
| `docs:` | none (visible) | Documentation |
| `test:` `chore:` `ci:` `build:` `style:` | none | hidden |
| `feat!:` or `BREAKING CHANGE:` in body | major | Features |

EVA ticket numbers go in the subject as a suffix, not a prefix:

```
feat: add dynamic command registration (EVA-9938)
fix: handle 401 retry without leaking response bodies (EVA-9938)
```

### Distribution surface (user-facing)

After a release, end users install via:

1. **Homebrew (macOS + Linux)** — primary:
   ```bash
   brew install headlinevc/tap/searchlight
   brew upgrade searchlight
   ```
2. **Direct GitHub Release download** — Windows + air-gapped:
   `searchlight_vX.Y.Z_<OS>_<arch>.{tar.gz,zip}` + `checksums.txt`.
3. **`go install`** — developers only:
   ```bash
   go install github.com/headlinevc/searchlight-cli@latest
   export SEARCHLIGHT_CLIENT_ID=<staging-client-id>
   ```
   `go install` does NOT honor GoReleaser `-ldflags`, so the baked-in
   `client_id` is empty — the user must set `SEARCHLIGHT_CLIENT_ID` manually.
   Not recommended for investor-facing installs.

### Pre-release setup (one-time, per environment)

Before the first release can succeed:

1. Create `headlinevc/homebrew-tap` repo (public, can be empty).
2. Create a PAT with `contents:write` on `homebrew-tap`, store as
   `HOMEBREW_TAP_TOKEN` secret on this repo.
3. Create the `OauthApplication` rows (see above) and stash
   `SEARCHLIGHT_PROD_CLIENT_ID` + `SEARCHLIGHT_STAGING_CLIENT_ID` secrets.

## Safety Rails

### NEVER

- Commit real client IDs or secrets to the repo. They live in GitHub
  Actions secrets and are injected only at release time via ldflags.
- Bypass CI hooks (`--no-verify`, `--no-gpg-sign`) on commits or pushes.
- Force-push to `main` or to a branch with an open release PR — release-please
  tracks state via PR labels and a manifest file.
- Hand-edit `.release-please-manifest.json` or `CHANGELOG.md` to bump versions.
  The bot owns both. Tag manually only as an emergency hotfix.
- Marshal an `oauth.Tokens` struct to anything that isn't the keyring backend.
  The `#nosec G117` annotation in `store.go` is scoped specifically to the
  store's Save method; never spread it elsewhere.
- Add real network calls to tests. Use `httptest.NewServer`. CI runners have
  no outbound network to private hosts.
- Re-introduce response-body leaks in retry paths. The `bodyclose` linter
  must remain green; see `internal/mcp/client.go` Call for the pattern.

### ALWAYS

- Run `go mod tidy` after touching imports. CI fails if `go.mod` / `go.sum`
  drift.
- Run `make test && make lint` before pushing. golangci-lint v1.59.1 is the
  CI baseline.
- Use conventional-commit prefixes (`feat:`, `fix:`, etc.) for everything on
  `main`. Without them, release-please ignores the commit when computing the
  next version.
- Honor existing exit code semantics when adding new error paths
  (`internal/errors/codes.go`). Agents script against these.
- Match HTTP timeouts to the operation. Tool calls: 120s. MCP client default:
  60s. OAuth callback: 5min. Token endpoint refresh: 30s.
- Update both per-property flag handling AND `--json` payload merging when
  changing the tool invocation surface — they're tested together in
  `cmd/integration_test.go`.

## Known Constraints / Open follow-ups

1. **`is_internal?` gate.** Every MCP request hits
   `lib/mcp/auth/authenticator.rb` which requires `user.is_internal? == true`.
   The CLI's intended investor audience is currently blocked by this. Needs a
   server-side change to scope access per `OauthApplication` type before
   external rollout. Tracked as a follow-up on EVA-9938 (separate ticket).
2. **`go install` UX.** Lacks the ldflags-injected client_id; the user falls
   back to the `SEARCHLIGHT_CLIENT_ID` env var. README + docs/agents.md
   explain this. Acceptable for dev workflows, not for investor onboarding.
3. **Browser loopback flow.** `oauth.Login.Run()` is not unit-testable
   without a fake browser launcher; coverage gap is acknowledged. Manual
   verification per the PR checklist covers it.
4. **Scoop / Windows native installer.** Not yet wired in `.goreleaser.yaml`
   even though Windows binaries are built. Add a `scoops:` block when a
   Windows audience materializes.
5. **SLSA provenance / cosign signing.** Not enabled. Worth adding before
   shipping to external investors who care about supply-chain integrity.

## Files to know

| File | Purpose |
|---|---|
| `main.go` | Entry point; wires version info, propagates `CodedError` exit codes |
| `cmd/root.go` | Globals struct, `setupGlobals`, `Execute`, cobra tree assembly |
| `cmd/dynamic.go` | `registerDynamicTools`, `buildToolCmd`, payload merge, dry-run heuristic |
| `cmd/tools.go` | `writeToolResult` — the JSON-stdout pass-through that agents depend on |
| `cmd/auth.go` | Login/logout/whoami/refresh; uses `oauth.Login.Run` and `oauth.Manager` |
| `internal/oauth/flow.go` | OAuth discovery + browser loopback; ephemeral port; redirect URI matching |
| `internal/oauth/token.go` | `Manager.AccessToken` — load → check expiry → refresh transparently → persist |
| `internal/oauth/store.go` | `KeyringStore`: persists the entire Tokens blob atomically |
| `internal/mcp/client.go` | JSON-RPC client; retry-once on 401 via `Tokens.ForceRefresh` |
| `internal/mcp/schema.go` | `SchemaCache.LoadOrFetch` — TTL + force-refresh; cache key includes server version |
| `internal/config/config.go` | `Load` — env-var precedence, build-time `prodClientID`/`stagingClientID` |
| `internal/errors/codes.go` | `CodedError`, `ExitCodeFor`, exit code constants |
| `internal/output/json.go` | stdout JSON / stderr humans; `--pretty` indentation; `--quiet` mute |
| `.goreleaser.yaml` | Cross-compile matrix, ldflags-injected client IDs, Homebrew tap publish |
| `release-please-config.json` | Release type, changelog sections, initial `release-as: "0.1.0"` |
| `.release-please-manifest.json` | Current version (`{".": "0.0.0"}` until first release) |
| `docs/agents.md` | SKILL.md-style invariants surfaced to AI agents driving the CLI |

## Verification

Before declaring work complete, confirm:

- [ ] `go vet ./...` clean
- [ ] `go test -race ./...` passes (offline; no network deps)
- [ ] `go test -coverprofile=... -coverpkg=./... ./...` aggregate ≥ 70%
- [ ] `golangci-lint run ./...` exits 0
- [ ] `gosec ./...` exits 0 (or new findings are annotated with rationale)
- [ ] `make build` succeeds with placeholder client IDs
- [ ] `./searchlight --help` lists `auth`, `tools`, `version` + global flags
- [ ] If touching OAuth or MCP transport: manual `auth login` against staging,
      then `auth whoami` round-trip
- [ ] If touching dynamic registration: `tools refresh` populates cache,
      `<tool-name> --help` renders parameter table

## Compact Instructions

When context compaction triggers, preserve in priority order:

1. Architecture decisions (NEVER summarize away — `is_internal?` constraint,
   dynamic registration design, conventional-commit requirement)
2. Modified files and their key changes
3. Current test pass/fail + coverage status
4. Open follow-ups (1–5 in "Known Constraints" above)
5. Tool outputs (delete; keep pass/fail status only)
