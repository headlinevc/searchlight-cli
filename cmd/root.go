package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/headlinevc/searchlight-cli/internal/config"
	"github.com/headlinevc/searchlight-cli/internal/keyring"
	"github.com/headlinevc/searchlight-cli/internal/mcp"
	"github.com/headlinevc/searchlight-cli/internal/oauth"
	"github.com/headlinevc/searchlight-cli/internal/output"
)

type Globals struct {
	Pretty  bool
	Quiet   bool
	NoCache bool
	Cfg     *config.Config
	HTTPC   *http.Client
	Tokens  *oauth.Manager
	MCP     *mcp.Client
	Schema  mcp.SchemaCache
}

var (
	globals     = &Globals{HTTPC: &http.Client{Timeout: 60 * time.Second}}
	versionInfo = struct {
		Version   string
		Commit    string
		BuildDate string
	}{"dev", "none", "unknown"}
)

func SetBuildInfo(v, c, d string) {
	versionInfo.Version = v
	versionInfo.Commit = c
	versionInfo.BuildDate = d
}

func newRootCmd() *cobra.Command {
	rc := &cobra.Command{
		Use:               "searchlight",
		Short:             "Searchlight CLI — a thin client over the Searchlight MCP server",
		SilenceUsage:      true,
		SilenceErrors:     true,
		DisableAutoGenTag: true,
	}

	rc.PersistentFlags().BoolVar(&globals.Pretty, "pretty", false, "pretty-print JSON output")
	rc.PersistentFlags().BoolVarP(&globals.Quiet, "quiet", "q", false, "suppress stderr progress/hints")
	rc.PersistentFlags().BoolVar(&globals.NoCache, "no-cache", false, "force refresh of tools/list cache")

	rc.AddCommand(
		newAuthCmd(),
		newToolsCmd(),
		newVersionCmd(),
	)
	return rc
}

// Execute is the entry point called from main.
//
// Order:
//  1. Build cobra tree with the static commands.
//  2. Attempt to load config + tokens + schema cache.
//  3. For dynamic tool invocations with a stale/missing cache and stored
//     credentials, re-fetch the schema (bounded) so the registry tracks the
//     server without manual refreshes. Failures stay silent only when a
//     usable cache exists to fall back to and --no-cache wasn't given;
//     otherwise the error propagates with its transport exit code.
//  4. Register dynamic tool subcommands from the cache (best-effort — if no
//     cache yet, top-level help still works; `searchlight auth login`
//     populates it automatically).
//  5. cobra parses argv and dispatches.
func Execute() error {
	root := newRootCmd()

	if err := setupGlobals(); err != nil {
		// Config errors shouldn't prevent --help from working. Surface them
		// later when a command actually needs the config.
		root.PersistentPreRunE = func(_ *cobra.Command, _ []string) error { return err }
	} else if globals.Schema.Path != "" {
		if err := ensureFreshSchema(); err != nil {
			return err
		}
		registerDynamicTools(root)
	}

	return root.Execute()
}

// staticCommands dispatch without the dynamic tool registry, so startup never
// needs a fresh schema for them — they must keep working offline and pre-auth
// with zero network calls. `tools` is in the set on purpose: it runs its own
// LoadOrFetch, and refreshing here too would double-fetch.
var staticCommands = map[string]bool{
	"help":       true,
	"version":    true,
	"auth":       true,
	"tools":      true,
	"completion": true,
}

// ensureFreshSchema keeps the dynamic command registry from drifting stale:
// when a dynamic tool invocation starts with a missing or expired cache and
// stored credentials exist, it re-fetches tools/list (bounded to a few
// seconds) before registration.
//
// Fetch failures are swallowed only when silence is honest: a usable cache
// exists to fall back to AND the user didn't demand freshness. With a
// stale-but-readable cache the old registry keeps working and exit codes are
// untouched (the designed best-effort path). But --no-cache is a promise that
// the schema was actually refreshed, and with no readable cache there is
// nothing to register — in both cases the error propagates, because a
// transport-coded failure is strictly more useful to a scripting agent than
// cobra's "unknown command".
func ensureFreshSchema() error {
	// The command word is the first arg that isn't a flag. All root persistent
	// flags are booleans (--pretty, -q/--quiet, --no-cache), so dash-prefixed
	// args never consume a following value and skipping them is safe — e.g.
	// `searchlight --pretty lookup_company` still classifies as a dynamic tool
	// invocation. No command word at all means a bare or flags-only invocation
	// (`searchlight --help`): nothing dynamic dispatches, so no fetch.
	//
	// `--no-cache` must also be sniffed here: this runs before cobra parses
	// persistent flags, so globals.NoCache is still false even when the flag
	// is on the command line. The docs promise the flag forces a schema
	// re-fetch on any command, so --no-cache anywhere in argv (before or
	// after the subcommand, bare or in cobra's --no-cache=<bool> form)
	// treats the cache as stale.
	first := ""
	noCache := false
	for _, arg := range os.Args[1:] {
		if arg == "--no-cache" {
			noCache = true
			continue
		}
		if v, ok := strings.CutPrefix(arg, "--no-cache="); ok {
			if b, err := strconv.ParseBool(v); err == nil && b {
				noCache = true
			}
			continue
		}
		if first == "" && !strings.HasPrefix(arg, "-") {
			first = arg
		}
	}
	// The `__` prefix covers cobra's internal commands (`__complete`,
	// `__completeNoDesc`): shell tab-completion fires these on every keystroke
	// and must never pay for a network fetch.
	if first == "" || staticCommands[first] || strings.HasPrefix(first, "__") {
		return nil
	}
	// Freshness before credentials: reading one cache file is cheaper than
	// touching the keyring, and the fresh-cache path is the common one.
	// fallbackUsable records whether a readable cache exists, which decides
	// below whether a fetch failure may be swallowed.
	fallbackUsable := false
	if !noCache {
		cs, err := globals.Schema.Load()
		if err == nil && globals.Schema.IsFresh(cs) {
			return nil
		}
		fallbackUsable = err == nil && cs != nil
	}
	if !hasStoredCredentials() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := globals.Schema.LoadOrFetch(ctx, schemaRefreshClient(), noCache); err != nil {
		if noCache || !fallbackUsable {
			return err
		}
	}
	return nil
}

// mcpClientWith clones globals.MCP's transport (server, UA, HTTP client) with
// an explicit token source. Callers pick the source that matches their trust
// context instead of inheriting whatever setupGlobals wired into globals.MCP.
func mcpClientWith(src mcp.TokenSource) *mcp.Client {
	return &mcp.Client{
		ServerURL:  globals.MCP.ServerURL,
		UserAgent:  globals.MCP.UserAgent,
		HTTPClient: globals.MCP.HTTPClient,
		Tokens:     src,
	}
}

// schemaRefreshClient returns the client for background schema fetches — the
// startup freshness probe and the post-login refresh. Two properties matter:
//
//  1. Same principal as future tool calls. When SEARCHLIGHT_TOKEN is set,
//     setupGlobals routes every tool call through the env token, and tool
//     availability can differ per principal (internal-user gate), so the
//     cached registry must be fetched as that same principal or the registry
//     and the executor would disagree until the next refresh.
//  2. Never interactive. globals.MCP's FallbackTokenSource reacts to a 401 by
//     warning on stderr and launching the 5-minute browser login, which a
//     silent bounded refresh must never do (the startup probe can even run
//     before cobra has parsed --quiet). A rejected env token therefore fails
//     here — silently at startup, with a stderr hint post-login — and the
//     interactive fallback fires on the next real tool call, where it
//     belongs. Without an env token, oauth.Manager is already
//     non-interactive (its refresh-token exchange honors the caller's
//     context), so it is used directly.
func schemaRefreshClient() *mcp.Client {
	if globals.Cfg.Token != "" {
		return mcpClientWith(staticTokenSource(globals.Cfg.Token))
	}
	return mcpClientWith(globals.Tokens)
}

// staticTokenSource serves a fixed pre-minted token. ForceRefresh is a no-op:
// MCP tokens don't rotate, and schema refreshes must not warn or fall back to
// a browser when the token is rejected.
type staticTokenSource string

func (s staticTokenSource) AccessToken(context.Context) (string, error) { return string(s), nil }

func (s staticTokenSource) ForceRefresh() {}

// hasStoredCredentials reports whether the CLI could authenticate without
// prompting: a pre-minted token from the environment, or tokens previously
// saved by `auth login`. No network is touched. It's a var so tests can stub
// out the OS-keyring read, which isn't hermetic on developer machines.
var hasStoredCredentials = func() bool {
	if globals.Cfg.Token != "" {
		return true
	}
	t, err := (oauth.KeyringStore{Backend: newKeyringBackend()}).Load()
	return err == nil && t != nil
}

func setupGlobals() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	globals.Cfg = cfg

	kr := keyring.New(cfg.CredentialsPath())
	store := oauth.KeyringStore{Backend: kr}

	// We don't know the token endpoint until we've discovered metadata,
	// but oauth.Manager is happy without it for the initial Load — refresh
	// will only fire if AccessToken is called and tokens are expired.
	tokenEndpoint := strings.TrimRight(cfg.ServerURL, "/") + "/oauth/token"
	globals.Tokens = oauth.NewManager(store, tokenEndpoint, cfg.ClientID, globals.HTTPC)

	// A pre-minted MCP token is sent as the Bearer directly, bypassing the OAuth
	// manager and keyring. If the server rejects it (revoked), the fallback warns
	// and — only on an interactive terminal — drops to the browser login flow.
	var tokenSrc mcp.TokenSource = globals.Tokens
	if cfg.Token != "" {
		tokenSrc = oauth.NewFallbackTokenSource(
			cfg.Token,
			output.IsTerminal(os.Stderr),
			func(msg string) { output.HumanF(globals.Quiet, "warning: %s", msg) },
			browserLogin(cfg, globals.HTTPC),
			func(t *oauth.Tokens) { _ = store.Save(t) },
		)
	}

	globals.MCP = &mcp.Client{
		ServerURL:  cfg.ServerURL,
		UserAgent:  fmt.Sprintf("searchlight-cli/%s", versionInfo.Version),
		HTTPClient: globals.HTTPC,
		Tokens:     tokenSrc,
	}
	// 1h TTL: server tools change near-daily, and the startup freshness check
	// plus the post-login refresh make a short TTL cheap (at most one bounded
	// re-fetch per hour per machine).
	globals.Schema = mcp.SchemaCache{
		Path: cfg.ToolsCachePath(""),
		TTL:  time.Hour,
	}
	return nil
}

// browserLogin returns a closure that runs the interactive OAuth flow, or nil
// when login isn't possible — without a client_id there's no way to drive OAuth,
// so a rejected token can only fail rather than fall back.
func browserLogin(cfg *config.Config, httpc *http.Client) func(context.Context) (*oauth.Tokens, error) {
	if cfg.ClientID == "" {
		return nil
	}
	return func(ctx context.Context) (*oauth.Tokens, error) {
		flow := oauth.Login{ServerURL: cfg.ServerURL, ClientID: cfg.ClientID, HTTPClient: httpc}
		res, err := flow.Run(ctx, "read")
		if err != nil {
			return nil, err
		}
		return res.Tokens, nil
	}
}
