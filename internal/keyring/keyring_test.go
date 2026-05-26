package keyring

import (
	"path/filepath"
	"testing"
)

// fileBackend tests exercise the file-fallback path only (not the OS keyring),
// since CI has no real keyring available. We touch the OSStore methods that
// the fallback delegates to.

func TestFileFallback_SetGetDelete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")
	s := New(path)

	if err := s.fileSet("alpha", "one"); err != nil {
		t.Fatalf("fileSet: %v", err)
	}
	if err := s.fileSet("beta", "two"); err != nil {
		t.Fatalf("fileSet: %v", err)
	}

	got, err := s.fileGet("alpha")
	if err != nil {
		t.Fatalf("fileGet alpha: %v", err)
	}
	if got != "one" {
		t.Errorf("alpha = %q, want one", got)
	}

	if err := s.fileDelete("alpha"); err != nil {
		t.Fatalf("fileDelete: %v", err)
	}
	if _, err := s.fileGet("alpha"); err == nil {
		t.Error("alpha should have been deleted")
	}

	got, err = s.fileGet("beta")
	if err != nil {
		t.Fatalf("fileGet beta after alpha delete: %v", err)
	}
	if got != "two" {
		t.Errorf("beta = %q, want two", got)
	}
}

func TestFileFallback_GetMissing(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "credentials.json"))
	_, err := s.fileGet("nope")
	if err != ErrNotFound {
		t.Errorf("missing key err = %v, want ErrNotFound", err)
	}
}
