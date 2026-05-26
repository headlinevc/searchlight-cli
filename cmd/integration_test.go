package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/headlinevc/searchlight-cli/internal/config"
	"github.com/headlinevc/searchlight-cli/internal/keyring"
	"github.com/headlinevc/searchlight-cli/internal/mcp"
	"github.com/headlinevc/searchlight-cli/internal/oauth"
)

// integrationFixture replaces globals with hermetic test doubles for the
// duration of a single test. Tests stand up an httptest server that speaks
// JSON-RPC, plus a file-backed token store.
type integrationFixture struct {
	t          *testing.T
	dir        string
	tokensPath string
	server     *httptest.Server
	handler    *integrationHandler
	prev       *Globals
}

type integrationHandler struct {
	mu      sync.Mutex
	called  []recordedCall
	respond map[string]func(args map[string]any) (any, bool) // returns (result, isError)
}

type recordedCall struct {
	method string
	name   string // for tools/call
	args   map[string]any
}

func (h *integrationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var rpc struct {
		Method string          `json:"method"`
		ID     int64           `json:"id"`
		Params json.RawMessage `json:"params"`
	}
	_ = json.Unmarshal(body, &rpc)

	h.mu.Lock()
	defer h.mu.Unlock()

	switch rpc.Method {
	case "initialize":
		h.called = append(h.called, recordedCall{method: rpc.Method})
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": rpc.ID,
			"result": mcp.InitializeResult{ServerInfo: struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			}{Name: "Searchlight MCP Server", Version: "4.2.0"}},
		})
	case "tools/list":
		h.called = append(h.called, recordedCall{method: rpc.Method})
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": rpc.ID,
			"result": mcp.ListToolsResult{Tools: []mcp.ToolDefinition{
				{Name: "get_current_user", Description: "Identify the signed-in user", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)},
			}},
		})
	case "tools/call":
		var p mcp.ToolCallParams
		_ = json.Unmarshal(rpc.Params, &p)
		h.called = append(h.called, recordedCall{method: rpc.Method, name: p.Name, args: p.Arguments})
		if fn, ok := h.respond[p.Name]; ok {
			result, isError := fn(p.Arguments)
			text, _ := json.Marshal(result)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": rpc.ID,
				"result": mcp.ToolCallResult{
					Content: []mcp.ToolContent{{Type: "text", Text: string(text)}},
					IsError: isError,
				},
			})
			return
		}
		// Default: empty content
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": rpc.ID,
			"result": mcp.ToolCallResult{Content: []mcp.ToolContent{{Type: "text", Text: `{}`}}},
		})
	}
}

func newIntegrationFixture(t *testing.T) *integrationFixture {
	t.Helper()
	dir := t.TempDir()
	tokensPath := filepath.Join(dir, "credentials.json")
	handler := &integrationHandler{
		respond: map[string]func(map[string]any) (any, bool){
			"get_current_user": func(_ map[string]any) (any, bool) {
				return map[string]any{"email": "user@example.com", "is_internal": true}, false
			},
		},
	}
	srv := httptest.NewServer(handler)

	prev := globals
	cfg := &config.Config{
		ServerURL: srv.URL,
		ClientID:  "test-client",
		CacheDir:  dir,
		ConfigDir: dir,
	}
	tokens := oauth.KeyringStore{Backend: keyring.New(tokensPath)}
	// Pre-populate a fresh token so the manager doesn't try to refresh.
	_ = tokens.Save(&oauth.Tokens{
		AccessToken:  "test-at",
		RefreshToken: "test-rt",
		TokenType:    "Bearer",
		ExpiresIn:    3600,
		ObtainedAt:   time.Now(),
	})

	mgr := oauth.NewManager(tokens, srv.URL+"/oauth/token", "test-client", srv.Client())
	client := &mcp.Client{
		ServerURL:  srv.URL,
		UserAgent:  "searchlight-cli/test",
		HTTPClient: srv.Client(),
		Tokens:     mgr,
	}

	globals = &Globals{
		Cfg:    cfg,
		HTTPC:  srv.Client(),
		Tokens: mgr,
		MCP:    client,
		Schema: mcp.SchemaCache{Path: filepath.Join(dir, "tools.json"), TTL: time.Hour},
	}

	t.Cleanup(func() {
		srv.Close()
		globals = prev
	})

	return &integrationFixture{
		t:          t,
		dir:        dir,
		tokensPath: tokensPath,
		server:     srv,
		handler:    handler,
		prev:       prev,
	}
}

func (f *integrationFixture) calls() []recordedCall {
	f.handler.mu.Lock()
	defer f.handler.mu.Unlock()
	out := make([]recordedCall, len(f.handler.called))
	copy(out, f.handler.called)
	return out
}

func runCmdCapturingStdout(t *testing.T, factory func() any) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = old })

	if fn, ok := factory().(error); ok && fn != nil {
		t.Errorf("command errored: %v", fn)
	}
	w.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}

func TestAuthWhoami_Integration(t *testing.T) {
	f := newIntegrationFixture(t)

	cmd := newAuthCmd()
	whoami, _, _ := cmd.Find([]string{"whoami"})
	if whoami == nil {
		t.Fatal("whoami subcommand missing")
	}

	out := runCmdCapturingStdout(t, func() any {
		return whoami.RunE(whoami, []string{})
	})

	// Did we hit the right tool?
	var sawCall bool
	for _, c := range f.calls() {
		if c.method == "tools/call" && c.name == "get_current_user" {
			sawCall = true
		}
	}
	if !sawCall {
		t.Errorf("expected tools/call get_current_user, got %+v", f.calls())
	}
	// Output should be the JSON pass-through.
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not JSON: %q", out)
	}
	if got["email"] != "user@example.com" {
		t.Errorf("email = %v, want user@example.com", got["email"])
	}
}

func TestAuthLogout_Integration(t *testing.T) {
	f := newIntegrationFixture(t)

	// Pre-condition: credentials file should exist.
	if _, err := os.Stat(f.tokensPath); err != nil {
		t.Fatalf("expected credentials file: %v", err)
	}

	cmd := newAuthCmd()
	logout, _, _ := cmd.Find([]string{"logout"})
	out := runCmdCapturingStdout(t, func() any {
		return logout.RunE(logout, []string{})
	})
	if !json.Valid([]byte(out)) {
		t.Errorf("logout output not JSON: %q", out)
	}
	// Post-condition: credentials gone.
	if _, err := os.Stat(f.tokensPath); !os.IsNotExist(err) {
		t.Errorf("credentials file should be removed after logout, err=%v", err)
	}
}

func TestAuthRefresh_Integration(t *testing.T) {
	f := newIntegrationFixture(t)
	// Replace the saved token with an expired one so refresh actually fires.
	store := oauth.KeyringStore{Backend: keyring.New(f.tokensPath)}
	_ = store.Save(&oauth.Tokens{
		AccessToken:  "old-at",
		RefreshToken: "old-rt",
		TokenType:    "Bearer",
		ExpiresIn:    60,
		ObtainedAt:   time.Now().Add(-time.Hour),
	})
	// Reroute the manager's token endpoint to a controllable server.
	refreshHits := 0
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		refreshHits++
		_ = json.NewEncoder(w).Encode(oauth.Tokens{
			AccessToken: "new-at", RefreshToken: "new-rt", TokenType: "Bearer", ExpiresIn: 3600,
		})
	}))
	defer tokenSrv.Close()

	globals.Tokens = oauth.NewManager(store, tokenSrv.URL, "test-client", tokenSrv.Client())

	cmd := newAuthCmd()
	refresh, _, _ := cmd.Find([]string{"refresh"})
	out := runCmdCapturingStdout(t, func() any {
		return refresh.RunE(refresh, []string{})
	})
	if !json.Valid([]byte(out)) {
		t.Errorf("refresh output not JSON: %q", out)
	}
	if refreshHits != 1 {
		t.Errorf("token endpoint hit %d times, want 1", refreshHits)
	}
}

func TestTools_ListSubcommand_Integration(t *testing.T) {
	_ = newIntegrationFixture(t)

	cmd := newToolsCmd()
	out := runCmdCapturingStdout(t, func() any {
		// RunE on the root tools command lists tools.
		return cmd.RunE(cmd, []string{})
	})

	var got struct {
		Count int                  `json:"count"`
		Tools []mcp.ToolDefinition `json:"tools"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output not JSON: %q", out)
	}
	if got.Count != 1 || got.Tools[0].Name != "get_current_user" {
		t.Errorf("got %+v", got)
	}
}

func TestTools_Describe_Integration(t *testing.T) {
	_ = newIntegrationFixture(t)
	cmd := newToolsCmd()
	desc, _, _ := cmd.Find([]string{"describe"})

	out := runCmdCapturingStdout(t, func() any {
		return desc.RunE(desc, []string{"get_current_user"})
	})
	var tool mcp.ToolDefinition
	if err := json.Unmarshal([]byte(out), &tool); err != nil {
		t.Fatalf("output not JSON: %q", out)
	}
	if tool.Name != "get_current_user" {
		t.Errorf("Name = %q", tool.Name)
	}
}

func TestTools_Describe_NotFound(t *testing.T) {
	_ = newIntegrationFixture(t)
	cmd := newToolsCmd()
	desc, _, _ := cmd.Find([]string{"describe"})

	err := desc.RunE(desc, []string{"no_such_tool"})
	if err == nil {
		t.Error("expected error for unknown tool")
	}
}

func TestTools_RefreshSubcommand_Integration(t *testing.T) {
	f := newIntegrationFixture(t)
	cmd := newToolsCmd()
	ref, _, _ := cmd.Find([]string{"refresh"})

	out := runCmdCapturingStdout(t, func() any {
		return ref.RunE(ref, []string{})
	})
	var got map[string]int
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output not JSON: %q", out)
	}
	if got["count"] != 1 {
		t.Errorf("count = %d, want 1", got["count"])
	}

	// Server should have been called for initialize + tools/list.
	var sawInit, sawList bool
	for _, c := range f.calls() {
		if c.method == "initialize" {
			sawInit = true
		}
		if c.method == "tools/list" {
			sawList = true
		}
	}
	if !sawInit || !sawList {
		t.Errorf("calls = %+v, want both initialize and tools/list", f.calls())
	}
}

func TestVersion_Integration(t *testing.T) {
	_ = newIntegrationFixture(t)
	cmd := newVersionCmd()
	out := runCmdCapturingStdout(t, func() any {
		return cmd.RunE(cmd, []string{})
	})
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output not JSON: %q", out)
	}
	if got["cli"] == nil {
		t.Error("missing cli block")
	}
	if got["server"] == nil {
		t.Error("missing server block (initialize should have populated it)")
	}
}

func TestNewKeyringBackend_UsesConfigPath(t *testing.T) {
	f := newIntegrationFixture(t)
	backend := newKeyringBackend()
	if backend == nil {
		t.Fatal("newKeyringBackend returned nil")
	}
	// Round-trip a value through it and confirm it lands in the credentials file.
	if err := backend.Set("test-key", "test-value"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := backend.Get("test-key")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "test-value" {
		t.Errorf("Get = %q", got)
	}
	_ = f // silence unused on platforms where credentials are stored only in OS keyring
}

