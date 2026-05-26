package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestSchemaCache_SaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cache := SchemaCache{Path: filepath.Join(dir, "tools.json"), TTL: time.Hour}

	in := &CachedSchema{
		ObtainedAt:    time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC),
		ServerVersion: "4.2.0",
		Tools: []ToolDefinition{
			{Name: "get_x", Description: "X", InputSchema: json.RawMessage(`{"type":"object"}`)},
		},
	}
	if err := cache.Save(in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err := cache.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out.ServerVersion != "4.2.0" {
		t.Errorf("version = %q, want 4.2.0", out.ServerVersion)
	}
	if len(out.Tools) != 1 || out.Tools[0].Name != "get_x" {
		t.Errorf("tools = %v", out.Tools)
	}
	if !out.ObtainedAt.Equal(in.ObtainedAt) {
		t.Errorf("ObtainedAt = %v, want %v", out.ObtainedAt, in.ObtainedAt)
	}
}

func TestSchemaCache_Load_MissingFile_ReturnsNilNoError(t *testing.T) {
	cache := SchemaCache{Path: filepath.Join(t.TempDir(), "nope.json"), TTL: time.Hour}
	out, err := cache.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out != nil {
		t.Errorf("Load = %v, want nil for missing file", out)
	}
}

func TestSchemaCache_LoadOrFetch_UsesFreshCache(t *testing.T) {
	dir := t.TempDir()
	cache := SchemaCache{Path: filepath.Join(dir, "tools.json"), TTL: time.Hour}
	_ = cache.Save(&CachedSchema{
		ObtainedAt:    time.Now(),
		ServerVersion: "1.0.0",
		Tools:         []ToolDefinition{{Name: "cached"}},
	})

	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		http.Error(w, "should not be called", http.StatusInternalServerError)
	}))
	defer srv.Close()
	client := &Client{ServerURL: srv.URL, HTTPClient: srv.Client()}

	tools, err := cache.LoadOrFetch(context.Background(), client, false)
	if err != nil {
		t.Fatalf("LoadOrFetch: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "cached" {
		t.Errorf("tools = %v, want one cached entry", tools)
	}
	if hits.Load() != 0 {
		t.Errorf("server hit %d times, want 0 (cache fresh)", hits.Load())
	}
}

func TestSchemaCache_LoadOrFetch_RefreshesWhenStale(t *testing.T) {
	dir := t.TempDir()
	cache := SchemaCache{Path: filepath.Join(dir, "tools.json"), TTL: time.Minute}
	_ = cache.Save(&CachedSchema{
		ObtainedAt:    time.Now().Add(-2 * time.Minute),
		ServerVersion: "old",
		Tools:         []ToolDefinition{{Name: "old"}},
	})

	srv := httptest.NewServer(initAndListHandler(t, "new", "fresh"))
	defer srv.Close()
	client := &Client{ServerURL: srv.URL, HTTPClient: srv.Client()}

	tools, err := cache.LoadOrFetch(context.Background(), client, false)
	if err != nil {
		t.Fatalf("LoadOrFetch: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "fresh" {
		t.Errorf("tools = %v, want fresh", tools)
	}
	got, _ := cache.Load()
	if got.ServerVersion != "new" {
		t.Errorf("cached version = %q, want new", got.ServerVersion)
	}
}

func TestSchemaCache_LoadOrFetch_ForceBypassesCache(t *testing.T) {
	dir := t.TempDir()
	cache := SchemaCache{Path: filepath.Join(dir, "tools.json"), TTL: time.Hour}
	_ = cache.Save(&CachedSchema{
		ObtainedAt:    time.Now(),
		ServerVersion: "old",
		Tools:         []ToolDefinition{{Name: "old"}},
	})

	srv := httptest.NewServer(initAndListHandler(t, "new", "fresh"))
	defer srv.Close()
	client := &Client{ServerURL: srv.URL, HTTPClient: srv.Client()}

	tools, err := cache.LoadOrFetch(context.Background(), client, true)
	if err != nil {
		t.Fatalf("LoadOrFetch force: %v", err)
	}
	if tools[0].Name != "fresh" {
		t.Errorf("force did not bypass cache: got %v", tools)
	}
}

// initAndListHandler dispatches initialize → ServerInfo.Version=version and
// tools/list → one ToolDefinition named toolName.
func initAndListHandler(t *testing.T, version, toolName string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var rpc struct {
			Method string `json:"method"`
			ID     int64  `json:"id"`
		}
		_ = json.Unmarshal(body, &rpc)
		switch rpc.Method {
		case "initialize":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": rpc.ID,
				"result": InitializeResult{ServerInfo: struct {
					Name    string `json:"name"`
					Version string `json:"version"`
				}{Version: version}},
			})
		case "tools/list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": rpc.ID,
				"result": ListToolsResult{Tools: []ToolDefinition{{Name: toolName}}},
			})
		}
	}
}
