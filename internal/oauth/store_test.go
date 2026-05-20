package oauth

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/headlinevc/searchlight-cli/internal/keyring"
)

func newFileStore(t *testing.T) KeyringStore {
	t.Helper()
	dir := t.TempDir()
	return KeyringStore{Backend: keyring.New(filepath.Join(dir, "credentials.json"))}
}

func TestKeyringStore_RoundTrip(t *testing.T) {
	store := newFileStore(t)
	in := &Tokens{
		AccessToken:  "at",
		RefreshToken: "rt",
		TokenType:    "Bearer",
		ExpiresIn:    900,
		ObtainedAt:   time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC),
	}
	if err := store.Save(in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out.AccessToken != "at" || out.RefreshToken != "rt" || out.ExpiresIn != 900 {
		t.Errorf("loaded = %+v", out)
	}
	if !out.ObtainedAt.Equal(in.ObtainedAt) {
		t.Errorf("ObtainedAt = %v, want %v", out.ObtainedAt, in.ObtainedAt)
	}
}

func TestKeyringStore_Load_Empty(t *testing.T) {
	store := newFileStore(t)
	out, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out != nil {
		t.Errorf("empty store should return nil, got %+v", out)
	}
}

func TestKeyringStore_Clear(t *testing.T) {
	store := newFileStore(t)
	_ = store.Save(&Tokens{AccessToken: "x"})
	if err := store.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	out, err := store.Load()
	if err != nil {
		t.Fatalf("Load after Clear: %v", err)
	}
	if out != nil {
		t.Errorf("after Clear, Load = %+v, want nil", out)
	}
}
