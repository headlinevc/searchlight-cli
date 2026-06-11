package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"

	sterr "github.com/headlinevc/searchlight-cli/internal/errors"
	"github.com/headlinevc/searchlight-cli/internal/mcp"
	"github.com/headlinevc/searchlight-cli/internal/oauth"
)

// setArgs swaps os.Args for the duration of the test — ensureFreshSchema peeks
// argv directly because it runs before cobra parses anything.
func setArgs(t *testing.T, args ...string) {
	t.Helper()
	prev := os.Args
	os.Args = args
	t.Cleanup(func() { os.Args = prev })
}

// stubCredentials replaces the keyring-backed credential check, which is not
// hermetic on developer machines (the real OS keychain may hold live tokens).
func stubCredentials(t *testing.T, present bool) {
	t.Helper()
	prev := hasStoredCredentials
	hasStoredCredentials = func() bool { return present }
	t.Cleanup(func() { hasStoredCredentials = prev })
}

// writeSchemaCacheFile seeds the fixture's schema cache with a single tool
// obtained at the given time, so tests control freshness precisely.
func writeSchemaCacheFile(t *testing.T, obtainedAt time.Time, toolName string) {
	t.Helper()
	err := globals.Schema.Save(&mcp.CachedSchema{
		ObtainedAt:    obtainedAt,
		ServerVersion: "4.1.0",
		Tools: []mcp.ToolDefinition{
			{Name: toolName, Description: "stale tool", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)},
		},
	})
	if err != nil {
		t.Fatalf("seed schema cache: %v", err)
	}
}

func countToolsListCalls(f *integrationFixture) int {
	n := 0
	for _, c := range f.calls() {
		if c.method == "tools/list" {
			n++
		}
	}
	return n
}

func registeredNames(root *cobra.Command) map[string]bool {
	out := map[string]bool{}
	for _, sub := range root.Commands() {
		out[sub.Name()] = true
	}
	return out
}

func TestEnsureFreshSchema_StaleCache_FetchesOnceAndReregisters(t *testing.T) {
	f := newIntegrationFixture(t)
	writeSchemaCacheFile(t, time.Now().Add(-2*time.Hour), "old_tool")
	stubCredentials(t, true)
	setArgs(t, "searchlight", "old_tool")

	if err := ensureFreshSchema(); err != nil {
		t.Fatalf("ensureFreshSchema: %v", err)
	}

	if got := countToolsListCalls(f); got != 1 {
		t.Errorf("tools/list called %d times, want exactly 1", got)
	}

	// Registration must reflect the fresh server payload, not the stale file.
	root := &cobra.Command{Use: "searchlight"}
	registerDynamicTools(root)
	names := registeredNames(root)
	if !names["get_current_user"] {
		t.Errorf("fresh tool get_current_user not registered, got %v", names)
	}
	if names["old_tool"] {
		t.Errorf("stale tool old_tool should be gone after refresh, got %v", names)
	}
}

func TestEnsureFreshSchema_MissingCache_Fetches(t *testing.T) {
	f := newIntegrationFixture(t)
	stubCredentials(t, true)
	setArgs(t, "searchlight", "get_current_user")

	if err := ensureFreshSchema(); err != nil {
		t.Fatalf("ensureFreshSchema: %v", err)
	}

	if got := countToolsListCalls(f); got != 1 {
		t.Errorf("tools/list called %d times, want exactly 1", got)
	}
	root := &cobra.Command{Use: "searchlight"}
	registerDynamicTools(root)
	if !registeredNames(root)["get_current_user"] {
		t.Error("expected get_current_user registered from freshly fetched cache")
	}
}

func TestEnsureFreshSchema_FreshCache_NoNetwork(t *testing.T) {
	f := newIntegrationFixture(t)
	writeSchemaCacheFile(t, time.Now(), "fresh_tool")
	stubCredentials(t, true)
	setArgs(t, "searchlight", "fresh_tool")

	ensureFreshSchema()

	if got := len(f.calls()); got != 0 {
		t.Errorf("expected zero network calls with a fresh cache, got %+v", f.calls())
	}
}

func TestEnsureFreshSchema_FetchFails_FallsBackToStaleCache(t *testing.T) {
	f := newIntegrationFixture(t)
	writeSchemaCacheFile(t, time.Now().Add(-2*time.Hour), "old_tool")
	stubCredentials(t, true)
	setArgs(t, "searchlight", "old_tool")

	// Server errors hard on every request.
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(failing.Close)
	globals.MCP = &mcp.Client{
		ServerURL:  failing.URL,
		UserAgent:  "searchlight-cli/test",
		HTTPClient: failing.Client(),
		Tokens:     globals.Tokens,
	}

	// A stale-but-usable cache exists and freshness wasn't forced: the
	// designed best-effort path swallows the failure.
	if err := ensureFreshSchema(); err != nil {
		t.Fatalf("expected silent fallback with a usable stale cache, got %v", err)
	}

	// The stale cache must survive and still drive registration.
	root := &cobra.Command{Use: "searchlight"}
	registerDynamicTools(root)
	if !registeredNames(root)["old_tool"] {
		t.Error("stale tool should remain registered when the refresh fails")
	}
	_ = f
}

func TestEnsureFreshSchema_ServerUnreachable_FallsBackToStaleCache(t *testing.T) {
	_ = newIntegrationFixture(t)
	writeSchemaCacheFile(t, time.Now().Add(-2*time.Hour), "old_tool")
	stubCredentials(t, true)
	setArgs(t, "searchlight", "old_tool")

	// Point at a server that is already closed: connection refused.
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()
	globals.MCP = &mcp.Client{
		ServerURL:  deadURL,
		UserAgent:  "searchlight-cli/test",
		HTTPClient: &http.Client{Timeout: time.Second},
		Tokens:     globals.Tokens,
	}

	if err := ensureFreshSchema(); err != nil {
		t.Fatalf("expected the transport error swallowed with a usable stale cache, got %v", err)
	}

	root := &cobra.Command{Use: "searchlight"}
	registerDynamicTools(root)
	if !registeredNames(root)["old_tool"] {
		t.Error("stale tool should remain registered when the server is unreachable")
	}
}

func TestEnsureFreshSchema_NoCredentials_NoNetwork(t *testing.T) {
	f := newIntegrationFixture(t)
	writeSchemaCacheFile(t, time.Now().Add(-2*time.Hour), "old_tool")
	stubCredentials(t, false)
	setArgs(t, "searchlight", "old_tool")

	ensureFreshSchema()

	if got := len(f.calls()); got != 0 {
		t.Errorf("expected zero network calls without credentials, got %+v", f.calls())
	}
}

func TestEnsureFreshSchema_StaticCommands_NoFetch(t *testing.T) {
	cases := [][]string{
		{"searchlight"},
		{"searchlight", "--help"},
		{"searchlight", "-q"},
		{"searchlight", "help"},
		{"searchlight", "version"},
		{"searchlight", "--pretty", "version"}, // leading flags don't hide a static command
		{"searchlight", "auth", "login"},
		{"searchlight", "tools"},
		{"searchlight", "completion", "zsh"},
		{"searchlight", "__complete", "lookup_company", ""}, // cobra-internal: tab-completion
		{"searchlight", "__completeNoDesc"},
		{"searchlight", "--no-cache", "version"}, // static skip wins over --no-cache
	}
	for _, args := range cases {
		t.Run(strings.Join(args[1:], " "), func(t *testing.T) {
			f := newIntegrationFixture(t)
			writeSchemaCacheFile(t, time.Now().Add(-2*time.Hour), "old_tool")
			stubCredentials(t, true)
			setArgs(t, args...)

			ensureFreshSchema()

			if got := len(f.calls()); got != 0 {
				t.Errorf("args %v: expected zero startup fetches, got %+v", args, f.calls())
			}
		})
	}
}

func TestEnsureFreshSchema_LeadingFlags_StillFetches(t *testing.T) {
	// Persistent flags before the command word must not mask a dynamic tool
	// invocation: cobra still dispatches to the tool, so the cache must be
	// fresh by then.
	cases := [][]string{
		{"searchlight", "--pretty", "lookup_company"},
		{"searchlight", "-q", "get_current_user"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args[1:], " "), func(t *testing.T) {
			f := newIntegrationFixture(t)
			writeSchemaCacheFile(t, time.Now().Add(-2*time.Hour), "old_tool")
			stubCredentials(t, true)
			setArgs(t, args...)

			ensureFreshSchema()

			if got := countToolsListCalls(f); got != 1 {
				t.Errorf("args %v: tools/list called %d times, want exactly 1", args, got)
			}
		})
	}
}

func TestEnsureFreshSchema_NoCacheFlag_ForcesFetchDespiteFreshCache(t *testing.T) {
	// --no-cache is parsed by cobra only after registration, so ensureFreshSchema
	// must sniff it from argv directly — wherever it sits relative to the
	// subcommand — to honor the documented "force refresh on any command".
	cases := [][]string{
		{"searchlight", "--no-cache", "lookup_company"},
		{"searchlight", "lookup_company", "--no-cache"},
		{"searchlight", "--no-cache=true", "lookup_company"}, // cobra's explicit bool form
	}
	for _, args := range cases {
		t.Run(strings.Join(args[1:], " "), func(t *testing.T) {
			f := newIntegrationFixture(t)
			writeSchemaCacheFile(t, time.Now(), "fresh_tool") // fresh: would normally short-circuit
			stubCredentials(t, true)
			setArgs(t, args...)

			ensureFreshSchema()

			if got := countToolsListCalls(f); got != 1 {
				t.Errorf("args %v: tools/list called %d times, want exactly 1", args, got)
			}
		})
	}
}

func TestEnsureFreshSchema_NoCacheFalse_FreshCacheSkipsFetch(t *testing.T) {
	f := newIntegrationFixture(t)
	writeSchemaCacheFile(t, time.Now(), "fresh_tool")
	stubCredentials(t, true)
	setArgs(t, "searchlight", "--no-cache=false", "lookup_company")

	ensureFreshSchema()

	if got := len(f.calls()); got != 0 {
		t.Errorf("--no-cache=false must not force a fetch on a fresh cache, got %+v", f.calls())
	}
}

func TestEnsureFreshSchema_NoCacheFlag_StillRequiresCredentials(t *testing.T) {
	f := newIntegrationFixture(t)
	writeSchemaCacheFile(t, time.Now(), "fresh_tool")
	stubCredentials(t, false)
	setArgs(t, "searchlight", "--no-cache", "lookup_company")

	ensureFreshSchema()

	if got := len(f.calls()); got != 0 {
		t.Errorf("expected zero network calls without credentials even with --no-cache, got %+v", f.calls())
	}
}

func TestEnsureFreshSchema_NoCacheForced_FetchFailure_PropagatesError(t *testing.T) {
	_ = newIntegrationFixture(t)
	writeSchemaCacheFile(t, time.Now(), "fresh_tool") // even a fresh cache can't satisfy --no-cache
	stubCredentials(t, true)
	setArgs(t, "searchlight", "--no-cache", "lookup_company")

	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(failing.Close)
	globals.MCP = &mcp.Client{
		ServerURL:  failing.URL,
		UserAgent:  "searchlight-cli/test",
		HTTPClient: failing.Client(),
		Tokens:     globals.Tokens,
	}

	err := ensureFreshSchema()
	if err == nil {
		t.Fatal("--no-cache promises an actual refresh; the fetch failure must propagate")
	}
	if got := sterr.ExitCodeFor(err); got != sterr.ExitTransport {
		t.Errorf("exit code = %d, want %d (transport)", got, sterr.ExitTransport)
	}
}

func TestEnsureFreshSchema_MissingCache_FetchFailure_PropagatesError(t *testing.T) {
	_ = newIntegrationFixture(t)
	stubCredentials(t, true)
	setArgs(t, "searchlight", "lookup_company")

	// No cache on disk and the server is unreachable: there is nothing to
	// register, so the agent must see the transport error rather than cobra's
	// "unknown command".
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()
	globals.MCP = &mcp.Client{
		ServerURL:  deadURL,
		UserAgent:  "searchlight-cli/test",
		HTTPClient: &http.Client{Timeout: time.Second},
		Tokens:     globals.Tokens,
	}

	err := ensureFreshSchema()
	if err == nil {
		t.Fatal("no cache to fall back to: the fetch failure must propagate")
	}
	if got := sterr.ExitCodeFor(err); got != sterr.ExitTransport {
		t.Errorf("exit code = %d, want %d (transport)", got, sterr.ExitTransport)
	}
}

func TestEnsureFreshSchema_RejectedPreMintedToken_NoInteractiveFallback(t *testing.T) {
	_ = newIntegrationFixture(t)
	writeSchemaCacheFile(t, time.Now().Add(-2*time.Hour), "old_tool")
	setArgs(t, "searchlight", "old_tool")

	// The pre-minted token has been revoked: the server 401s everything.
	var hits atomic.Int32
	reject := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(reject.Close)

	// globals.MCP carries the interactive FallbackTokenSource exactly as
	// setupGlobals wires it when SEARCHLIGHT_TOKEN is set. The startup probe
	// must NOT route through it — a warning or browser-login attempt from the
	// probe is the bug this test pins.
	var warned, loginCalled atomic.Bool
	fallback := oauth.NewFallbackTokenSource(
		"revoked-token",
		true, // interactive terminal: the fallback WOULD open a browser if asked
		func(string) { warned.Store(true) },
		func(context.Context) (*oauth.Tokens, error) {
			loginCalled.Store(true)
			return nil, errors.New("browser login must not run during the startup probe")
		},
		nil,
	)
	globals.MCP = &mcp.Client{
		ServerURL:  reject.URL,
		UserAgent:  "searchlight-cli/test",
		HTTPClient: reject.Client(),
		Tokens:     fallback,
	}
	prevTok := globals.Cfg.Token
	globals.Cfg.Token = "revoked-token" // also satisfies hasStoredCredentials
	t.Cleanup(func() { globals.Cfg.Token = prevTok })

	start := time.Now()
	err := ensureFreshSchema()
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("expected silent fallback (usable stale cache, no force), got %v", err)
	}
	if loginCalled.Load() {
		t.Error("startup probe launched the interactive browser login")
	}
	if warned.Load() {
		t.Error("startup probe emitted fallback warnings")
	}
	if hits.Load() == 0 {
		t.Error("expected the probe to attempt the fetch (stale cache + token present)")
	}
	if elapsed > 4*time.Second {
		t.Errorf("probe took %v, want well under the 5-minute browser window", elapsed)
	}
	// The stale cache survives the failed probe and still drives registration.
	root := &cobra.Command{Use: "searchlight"}
	registerDynamicTools(root)
	if !registeredNames(root)["old_tool"] {
		t.Error("stale tool should remain registered after the rejected-token probe")
	}
}

func TestHasStoredCredentials_PreMintedToken(t *testing.T) {
	_ = newIntegrationFixture(t)
	prev := globals.Cfg.Token
	globals.Cfg.Token = "pre-minted-mcp-token"
	t.Cleanup(func() { globals.Cfg.Token = prev })

	if !hasStoredCredentials() {
		t.Error("SEARCHLIGHT_TOKEN-style config token should count as credentials")
	}
}

func TestRefreshSchemaAfterLogin_Success_WritesCache(t *testing.T) {
	f := newIntegrationFixture(t)

	refreshSchemaAfterLogin()

	if got := countToolsListCalls(f); got != 1 {
		t.Errorf("tools/list called %d times, want 1", got)
	}
	cs, err := globals.Schema.Load()
	if err != nil || cs == nil {
		t.Fatalf("expected cache written after login refresh, got cs=%v err=%v", cs, err)
	}
	if len(cs.Tools) != 1 || cs.Tools[0].Name != "get_current_user" {
		t.Errorf("cached tools = %+v, want [get_current_user]", cs.Tools)
	}
}

func TestRefreshSchemaAfterLogin_NoEnvToken_UsesOAuthTokens(t *testing.T) {
	f := newIntegrationFixture(t)

	// Without SEARCHLIGHT_TOKEN, future tool calls run as the OAuth principal,
	// so the post-login cache must be fetched with the just-saved tokens (the
	// fixture manager serves "test-at").
	refreshSchemaAfterLogin()

	calls := f.calls()
	if len(calls) == 0 {
		t.Fatal("expected the post-login refresh to hit the server")
	}
	for _, c := range calls {
		if c.auth != "Bearer test-at" {
			t.Errorf("%s request used auth %q, want \"Bearer test-at\" (the freshly saved OAuth token)", c.method, c.auth)
		}
	}
}

func TestRefreshSchemaAfterLogin_EnvTokenSet_FetchesAsEnvTokenPrincipal(t *testing.T) {
	f := newIntegrationFixture(t)

	// Simulate the SEARCHLIGHT_TOKEN wiring: globals.MCP carries the
	// interactive FallbackTokenSource for the env token. Future tool calls run
	// as that principal — and tool availability can differ per principal — so
	// the post-login cache must be fetched as the env token too. It must still
	// not route through the fallback source: a revoked token warns and exits 0,
	// never re-opening the browser from inside `auth login`.
	var warned, loginCalled atomic.Bool
	globals.MCP.Tokens = oauth.NewFallbackTokenSource(
		"env-token",
		true,
		func(string) { warned.Store(true) },
		func(context.Context) (*oauth.Tokens, error) {
			loginCalled.Store(true)
			return nil, errors.New("browser login must not run after auth login")
		},
		nil,
	)
	prevTok := globals.Cfg.Token
	globals.Cfg.Token = "env-token"
	t.Cleanup(func() { globals.Cfg.Token = prevTok })

	refreshSchemaAfterLogin()

	if warned.Load() || loginCalled.Load() {
		t.Error("post-login refresh routed through the interactive fallback source")
	}
	calls := f.calls()
	if len(calls) == 0 {
		t.Fatal("expected the post-login refresh to hit the server")
	}
	for _, c := range calls {
		if c.auth != "Bearer env-token" {
			t.Errorf("%s request used auth %q, want \"Bearer env-token\" (the principal future tool calls use)", c.method, c.auth)
		}
	}
	cs, err := globals.Schema.Load()
	if err != nil || cs == nil || len(cs.Tools) != 1 || cs.Tools[0].Name != "get_current_user" {
		t.Errorf("cache after login refresh = %+v (err=%v), want [get_current_user]", cs, err)
	}
}

func TestRefreshSchemaAfterLogin_FailureIsNonFatal(t *testing.T) {
	_ = newIntegrationFixture(t)
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(failing.Close)
	globals.MCP = &mcp.Client{
		ServerURL:  failing.URL,
		UserAgent:  "searchlight-cli/test",
		HTTPClient: failing.Client(),
		Tokens:     globals.Tokens,
	}

	// Must not panic or propagate an error — login exits 0 even when the
	// schema refresh fails; the warning goes to stderr only.
	refreshSchemaAfterLogin()

	cs, err := globals.Schema.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cs != nil {
		t.Errorf("no cache should be written on refresh failure, got %+v", cs)
	}
}
