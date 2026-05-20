package cmd

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/headlinevc/searchlight-cli/internal/mcp"
)

func TestSetBuildInfo(t *testing.T) {
	prev := versionInfo
	t.Cleanup(func() { versionInfo = prev })

	SetBuildInfo("1.2.3", "abcdef", "2026-05-20")
	if versionInfo.Version != "1.2.3" {
		t.Errorf("Version = %q, want 1.2.3", versionInfo.Version)
	}
	if versionInfo.Commit != "abcdef" {
		t.Errorf("Commit = %q, want abcdef", versionInfo.Commit)
	}
	if versionInfo.BuildDate != "2026-05-20" {
		t.Errorf("BuildDate = %q, want 2026-05-20", versionInfo.BuildDate)
	}
}

func TestNewRootCmd_HasExpectedSubcommands(t *testing.T) {
	root := newRootCmd()
	want := map[string]bool{
		"auth":    false,
		"tools":   false,
		"version": false,
	}
	for _, sub := range root.Commands() {
		if _, ok := want[sub.Name()]; ok {
			want[sub.Name()] = true
		}
	}
	for name, present := range want {
		if !present {
			t.Errorf("expected subcommand %q to be registered", name)
		}
	}
}

func TestNewRootCmd_GlobalFlagsRegistered(t *testing.T) {
	root := newRootCmd()
	for _, name := range []string{"pretty", "quiet", "no-cache"} {
		if root.PersistentFlags().Lookup(name) == nil {
			t.Errorf("global flag --%s not registered", name)
		}
	}
}

func TestRegisterDynamicTools_AddsCommandsFromCache(t *testing.T) {
	// Build a fresh schema cache file and point globals at it.
	dir := t.TempDir()
	path := filepath.Join(dir, "tools.json")
	cache := mcp.SchemaCache{Path: path, TTL: time.Hour}
	in := &mcp.CachedSchema{
		ObtainedAt:    time.Now(),
		ServerVersion: "4.2.0",
		Tools: []mcp.ToolDefinition{
			{Name: "get_x", Description: "X", InputSchema: json.RawMessage(`{"type":"object"}`)},
			{Name: "send_y", Description: "Y", InputSchema: json.RawMessage(`{"type":"object"}`)},
		},
	}
	if err := cache.Save(in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	prev := globals.Schema
	globals.Schema = cache
	t.Cleanup(func() { globals.Schema = prev })

	root := &cobra.Command{Use: "searchlight"}
	registerDynamicTools(root)

	found := map[string]bool{}
	for _, sub := range root.Commands() {
		found[sub.Name()] = true
	}
	if !found["get_x"] || !found["send_y"] {
		t.Errorf("expected dynamic commands get_x + send_y, got %v", found)
	}
}

func TestRegisterDynamicTools_NoCache_NoOp(t *testing.T) {
	prev := globals.Schema
	globals.Schema = mcp.SchemaCache{Path: filepath.Join(t.TempDir(), "missing.json"), TTL: time.Hour}
	t.Cleanup(func() { globals.Schema = prev })

	root := &cobra.Command{Use: "searchlight"}
	registerDynamicTools(root) // should not panic

	if len(root.Commands()) != 0 {
		t.Errorf("expected no commands when cache absent, got %d", len(root.Commands()))
	}
}

func TestRegisterDynamicTools_SkipsExistingNames(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tools.json")
	cache := mcp.SchemaCache{Path: path, TTL: time.Hour}
	_ = cache.Save(&mcp.CachedSchema{
		ObtainedAt:    time.Now(),
		ServerVersion: "4.2.0",
		Tools:         []mcp.ToolDefinition{{Name: "tools", Description: "would collide", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	})
	prev := globals.Schema
	globals.Schema = cache
	t.Cleanup(func() { globals.Schema = prev })

	root := &cobra.Command{Use: "searchlight"}
	root.AddCommand(&cobra.Command{Use: "tools"})

	registerDynamicTools(root)

	// We should still have only one subcommand named "tools" — the pre-existing one.
	count := 0
	for _, sub := range root.Commands() {
		if sub.Name() == "tools" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 'tools' subcommand (collision skipped), got %d", count)
	}
}
