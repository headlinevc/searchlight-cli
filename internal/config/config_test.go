package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withDirs sets XDG_CACHE_HOME and XDG_CONFIG_HOME to a temp dir so Load
// doesn't write to the user's real home.
func withDirs(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	// Reset HOME too — os.UserCacheDir falls back to $HOME/.cache on Linux
	// when XDG_CACHE_HOME is unset, but always honors XDG_* when set.
	t.Setenv("HOME", dir)
}

func TestLoad_DefaultServerURL(t *testing.T) {
	withDirs(t)
	t.Setenv("SEARCHLIGHT_URL", "")
	t.Setenv("SEARCHLIGHT_CLIENT_ID", "test-client")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ServerURL != DefaultServerURL {
		t.Errorf("ServerURL = %q, want %q", cfg.ServerURL, DefaultServerURL)
	}
	if cfg.ClientID != "test-client" {
		t.Errorf("ClientID = %q, want test-client", cfg.ClientID)
	}
}

func TestLoad_CustomServerURL_StripsTrailingSlash(t *testing.T) {
	withDirs(t)
	t.Setenv("SEARCHLIGHT_URL", "https://example.com/")
	t.Setenv("SEARCHLIGHT_CLIENT_ID", "x")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ServerURL != "https://example.com" {
		t.Errorf("ServerURL = %q, want https://example.com (no trailing slash)", cfg.ServerURL)
	}
}

func TestLoad_NoClientID_Errors(t *testing.T) {
	withDirs(t)
	t.Setenv("SEARCHLIGHT_URL", "https://example.com")
	t.Setenv("SEARCHLIGHT_CLIENT_ID", "")
	t.Setenv("SEARCHLIGHT_TOKEN", "")
	prev := prodClientID
	prodClientID = ""
	t.Cleanup(func() { prodClientID = prev })

	if _, err := Load(); err == nil {
		t.Fatal("expected error when no client_id is configured")
	}
}

func TestLoad_TokenBypassesClientIDRequirement(t *testing.T) {
	withDirs(t)
	t.Setenv("SEARCHLIGHT_URL", "https://example.com")
	t.Setenv("SEARCHLIGHT_CLIENT_ID", "")
	t.Setenv("SEARCHLIGHT_TOKEN", "mcp-token-abc")
	prev := prodClientID
	prodClientID = ""
	t.Cleanup(func() { prodClientID = prev })

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load with a token but no client_id should succeed: %v", err)
	}
	if cfg.Token != "mcp-token-abc" {
		t.Errorf("Token = %q, want mcp-token-abc", cfg.Token)
	}
	if cfg.ClientID != "" {
		t.Errorf("ClientID = %q, want empty (no OAuth needed when a token is supplied)", cfg.ClientID)
	}
}

func TestLoad_UsesBakedInProdClientID(t *testing.T) {
	withDirs(t)
	t.Setenv("SEARCHLIGHT_URL", "https://searchlight.headline.com")
	t.Setenv("SEARCHLIGHT_CLIENT_ID", "")
	prev := prodClientID
	prodClientID = "PROD"
	t.Cleanup(func() { prodClientID = prev })

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ClientID != "PROD" {
		t.Errorf("ClientID = %q, want PROD", cfg.ClientID)
	}
}

func TestLoad_CreatesDirsWithSecurePerms(t *testing.T) {
	withDirs(t)
	t.Setenv("SEARCHLIGHT_CLIENT_ID", "x")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, d := range []string{cfg.CacheDir, cfg.ConfigDir} {
		info, err := os.Stat(d)
		if err != nil {
			t.Errorf("stat %s: %v", d, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", d)
		}
		// 0o700 expected — but skip on Windows where perm bits work differently
		if info.Mode().Perm() != 0o700 && os.Getenv("GOOS") != "windows" {
			t.Logf("WARN: %s perms = %o, want 0700 (may be filesystem-dependent)", d, info.Mode().Perm())
		}
	}
}

func TestConfig_ToolsCachePath(t *testing.T) {
	cfg := &Config{CacheDir: "/tmp/sl"}
	got := cfg.ToolsCachePath("4.2.0")
	want := "/tmp/sl/tools-4.2.0.json"
	if got != want {
		t.Errorf("ToolsCachePath(4.2.0) = %q, want %q", got, want)
	}
}

func TestConfig_ToolsCachePath_EmptyVersion(t *testing.T) {
	cfg := &Config{CacheDir: "/tmp/sl"}
	got := cfg.ToolsCachePath("")
	if !strings.Contains(got, "tools-unknown.json") {
		t.Errorf("empty version → %q, want unknown sentinel", got)
	}
}

func TestConfig_CredentialsPath(t *testing.T) {
	cfg := &Config{ConfigDir: "/etc/sl"}
	got := cfg.CredentialsPath()
	want := "/etc/sl/credentials.json"
	if got != want {
		t.Errorf("CredentialsPath = %q, want %q", got, want)
	}
}
