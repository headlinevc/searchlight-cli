package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	sterr "github.com/headlinevc/searchlight-cli/internal/errors"
)

// fakeTokens lets tests dictate the bearer token returned to the client and
// observe how many times ForceRefresh was called.
type fakeTokens struct {
	token        string
	refreshAfter string
	calls        atomic.Int64
	refreshCalls atomic.Int64
}

func (f *fakeTokens) AccessToken(_ context.Context) (string, error) {
	n := f.calls.Add(1)
	if f.refreshAfter != "" && n > 1 {
		return f.refreshAfter, nil
	}
	return f.token, nil
}

func (f *fakeTokens) ForceRefresh() { f.refreshCalls.Add(1) }

// recordingHandler captures every request the test client sends.
type recordingHandler struct {
	requests []recordedRequest
	handler  http.HandlerFunc
}

type recordedRequest struct {
	method string
	auth   string
	body   string
}

func (h *recordingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	h.requests = append(h.requests, recordedRequest{
		method: r.Method,
		auth:   r.Header.Get("Authorization"),
		body:   string(body),
	})
	h.handler(w, r)
}

func newTestClient(t *testing.T, handler http.HandlerFunc, tokens TokenSource) (*Client, *recordingHandler, func()) {
	t.Helper()
	rec := &recordingHandler{handler: handler}
	srv := httptest.NewServer(rec)
	c := &Client{
		ServerURL:  srv.URL,
		UserAgent:  "test",
		HTTPClient: srv.Client(),
		Tokens:     tokens,
	}
	return c, rec, srv.Close
}

func writeRPCResult(t *testing.T, w http.ResponseWriter, id int64, result any) {
	t.Helper()
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  json.RawMessage(raw),
	})
}

func TestClient_Call_Success(t *testing.T) {
	c, rec, cleanup := newTestClient(t,
		func(w http.ResponseWriter, _ *http.Request) {
			writeRPCResult(t, w, 1, map[string]string{"hello": "world"})
		},
		&fakeTokens{token: "abc"},
	)
	defer cleanup()

	raw, err := c.Call(context.Background(), "any/method", map[string]string{"foo": "bar"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if got["hello"] != "world" {
		t.Errorf("result = %v, want hello=world", got)
	}
	if len(rec.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(rec.requests))
	}
	req := rec.requests[0]
	if req.method != http.MethodPost {
		t.Errorf("method = %s, want POST", req.method)
	}
	if req.auth != "Bearer abc" {
		t.Errorf("auth = %q, want Bearer abc", req.auth)
	}
	if !strings.Contains(req.body, `"method":"any/method"`) {
		t.Errorf("body missing method, got: %s", req.body)
	}
}

func TestClient_Call_RPCError(t *testing.T) {
	c, _, cleanup := newTestClient(t,
		func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"error":   map[string]any{"code": -32601, "message": "method not found"},
			})
		},
		&fakeTokens{token: "abc"},
	)
	defer cleanup()

	_, err := c.Call(context.Background(), "bogus", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var ce *sterr.CodedError
	if !errors.As(err, &ce) {
		t.Fatalf("err is not CodedError: %v", err)
	}
	if ce.Exit != sterr.ExitGeneralFailure {
		t.Errorf("exit = %d, want %d", ce.Exit, sterr.ExitGeneralFailure)
	}
}

func TestClient_Call_4xxNonAuth(t *testing.T) {
	c, _, cleanup := newTestClient(t,
		func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "not found", http.StatusNotFound)
		},
		&fakeTokens{token: "abc"},
	)
	defer cleanup()

	_, err := c.Call(context.Background(), "any", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if sterr.ExitCodeFor(err) != sterr.ExitTransport {
		t.Errorf("exit = %d, want %d", sterr.ExitCodeFor(err), sterr.ExitTransport)
	}
}

func TestClient_Call_401WithRefresh_Succeeds(t *testing.T) {
	var attempt atomic.Int64
	c, rec, cleanup := newTestClient(t,
		func(w http.ResponseWriter, _ *http.Request) {
			n := attempt.Add(1)
			if n == 1 {
				http.Error(w, "expired", http.StatusUnauthorized)
				return
			}
			writeRPCResult(t, w, 2, map[string]string{"ok": "yes"})
		},
		&fakeTokens{token: "old", refreshAfter: "new"},
	)
	defer cleanup()

	raw, err := c.Call(context.Background(), "any", nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var got map[string]string
	_ = json.Unmarshal(raw, &got)
	if got["ok"] != "yes" {
		t.Errorf("result = %v, want ok=yes", got)
	}
	if len(rec.requests) != 2 {
		t.Fatalf("requests = %d, want 2 (initial + retry)", len(rec.requests))
	}
	if rec.requests[0].auth != "Bearer old" {
		t.Errorf("first auth = %q, want Bearer old", rec.requests[0].auth)
	}
	if rec.requests[1].auth != "Bearer new" {
		t.Errorf("retry auth = %q, want Bearer new", rec.requests[1].auth)
	}
}

func TestClient_Call_401Persisted_ReturnsPermissionDenied(t *testing.T) {
	c, _, cleanup := newTestClient(t,
		func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "still expired", http.StatusUnauthorized)
		},
		&fakeTokens{token: "old", refreshAfter: "new"},
	)
	defer cleanup()

	_, err := c.Call(context.Background(), "any", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if sterr.ExitCodeFor(err) != sterr.ExitPermissionDenied {
		t.Errorf("exit = %d, want %d", sterr.ExitCodeFor(err), sterr.ExitPermissionDenied)
	}
}

func TestClient_Call_403_NoRetry(t *testing.T) {
	var attempt atomic.Int64
	c, _, cleanup := newTestClient(t,
		func(w http.ResponseWriter, _ *http.Request) {
			attempt.Add(1)
			http.Error(w, "forbidden", http.StatusForbidden)
		},
		&fakeTokens{token: "tok"},
	)
	defer cleanup()

	_, err := c.Call(context.Background(), "any", nil)
	if sterr.ExitCodeFor(err) != sterr.ExitPermissionDenied {
		t.Errorf("exit = %d, want %d", sterr.ExitCodeFor(err), sterr.ExitPermissionDenied)
	}
	if attempt.Load() != 1 {
		t.Errorf("attempts = %d, want 1 (no retry on 403)", attempt.Load())
	}
}

func TestClient_Call_NoTokenSource_OmitsAuthHeader(t *testing.T) {
	c, rec, cleanup := newTestClient(t,
		func(w http.ResponseWriter, _ *http.Request) {
			writeRPCResult(t, w, 1, map[string]string{"x": "y"})
		},
		nil,
	)
	defer cleanup()

	if _, err := c.Call(context.Background(), "any", nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if rec.requests[0].auth != "" {
		t.Errorf("auth = %q, want empty when no token source", rec.requests[0].auth)
	}
}

func TestClient_ListTools(t *testing.T) {
	c, _, cleanup := newTestClient(t,
		func(w http.ResponseWriter, _ *http.Request) {
			writeRPCResult(t, w, 1, ListToolsResult{
				Tools: []ToolDefinition{
					{Name: "get_x", Description: "X", InputSchema: json.RawMessage(`{"type":"object"}`)},
					{Name: "send_y", Description: "Y", InputSchema: json.RawMessage(`{"type":"object"}`)},
				},
			})
		},
		nil,
	)
	defer cleanup()

	out, err := c.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(out.Tools) != 2 {
		t.Fatalf("tools = %d, want 2", len(out.Tools))
	}
	if out.Tools[0].Name != "get_x" || out.Tools[1].Name != "send_y" {
		t.Errorf("got %v", out.Tools)
	}
}

func TestClient_CallTool(t *testing.T) {
	c, rec, cleanup := newTestClient(t,
		func(w http.ResponseWriter, _ *http.Request) {
			writeRPCResult(t, w, 1, ToolCallResult{
				Content: []ToolContent{{Type: "text", Text: `{"result":42}`}},
				IsError: false,
			})
		},
		nil,
	)
	defer cleanup()

	res, err := c.CallTool(context.Background(), "lookup_company", map[string]any{"domain": "openai.com"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Error("IsError = true, want false")
	}
	if len(res.Content) != 1 || res.Content[0].Text != `{"result":42}` {
		t.Errorf("content = %v", res.Content)
	}
	if !strings.Contains(rec.requests[0].body, `"name":"lookup_company"`) {
		t.Errorf("request body missing name: %s", rec.requests[0].body)
	}
	if !strings.Contains(rec.requests[0].body, `"domain":"openai.com"`) {
		t.Errorf("request body missing args: %s", rec.requests[0].body)
	}
}

func TestClient_Initialize(t *testing.T) {
	c, _, cleanup := newTestClient(t,
		func(w http.ResponseWriter, _ *http.Request) {
			writeRPCResult(t, w, 1, InitializeResult{
				ProtocolVersion: "2024-11-05",
				ServerInfo: struct {
					Name    string `json:"name"`
					Version string `json:"version"`
				}{Name: "Searchlight MCP Server", Version: "4.2.0"},
			})
		},
		nil,
	)
	defer cleanup()

	init, err := c.Initialize(context.Background())
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if init.ServerInfo.Version != "4.2.0" {
		t.Errorf("version = %q, want 4.2.0", init.ServerInfo.Version)
	}
}

func TestClient_Call_TransportError(t *testing.T) {
	// Use a URL pointing at a port nothing's listening on. net/http returns
	// a connection-refused error which the client should wrap as transport.
	c := &Client{
		ServerURL:  "http://127.0.0.1:1", // privileged port, nothing listens
		HTTPClient: &http.Client{},
	}
	_, err := c.Call(context.Background(), "any", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if sterr.ExitCodeFor(err) != sterr.ExitTransport {
		t.Errorf("exit = %d, want %d", sterr.ExitCodeFor(err), sterr.ExitTransport)
	}
}
