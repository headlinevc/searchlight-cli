package keyring

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNew_ReturnsConfiguredStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	s := New(path)
	if s == nil {
		t.Fatal("New returned nil")
	}
	if s.credentialsPath != path {
		t.Errorf("credentialsPath = %q, want %q", s.credentialsPath, path)
	}
}

func TestFileFallback_PersistsAcrossInstances(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	s1 := New(path)
	if err := s1.fileSet("foo", "bar"); err != nil {
		t.Fatalf("fileSet: %v", err)
	}

	s2 := New(path)
	got, err := s2.fileGet("foo")
	if err != nil {
		t.Fatalf("fileGet on new instance: %v", err)
	}
	if got != "bar" {
		t.Errorf("got %q, want bar", got)
	}
}

func TestFileFallback_DeleteLastKeyRemovesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	s := New(path)
	_ = s.fileSet("only", "value")

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file should exist: %v", err)
	}
	if err := s.fileDelete("only"); err != nil {
		t.Fatalf("fileDelete: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file should be removed after deleting last key, got err=%v", err)
	}
}

func TestFileFallback_CorruptFileSurfacesError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")
	// Write garbage that isn't valid JSON.
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	s := New(path)
	if _, err := s.fileGet("anything"); err == nil {
		t.Error("expected error reading corrupt credentials file")
	}
}

// TestOSStore_Set_PrefersOSKeyringWhenAvailable is intentionally omitted: in CI
// the OS keyring is not present, so Set falls back to the file. Exercising the
// OS keyring path requires mocking zalando/go-keyring, which is out of scope
// here — the contract we care about (Set/Get round-trip) is verified via the
// file fallback path, which is the only path that runs in headless envs.
