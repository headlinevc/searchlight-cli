package oauth

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestNewPKCE_S256RoundTrip(t *testing.T) {
	pkce, err := NewPKCE()
	if err != nil {
		t.Fatalf("NewPKCE: %v", err)
	}
	if pkce.Method != "S256" {
		t.Errorf("Method = %q, want S256", pkce.Method)
	}
	if got := len(pkce.Verifier); got != 43 {
		// 32 bytes base64url-no-pad = 43 chars
		t.Errorf("Verifier length = %d, want 43", got)
	}
	if got := len(pkce.Challenge); got != 43 {
		// SHA256 (32 bytes) base64url-no-pad = 43 chars
		t.Errorf("Challenge length = %d, want 43", got)
	}
	sum := sha256.Sum256([]byte(pkce.Verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if pkce.Challenge != want {
		t.Errorf("Challenge does not match SHA256(Verifier)")
	}
}

func TestNewPKCE_UniquePerCall(t *testing.T) {
	a, _ := NewPKCE()
	b, _ := NewPKCE()
	if a.Verifier == b.Verifier {
		t.Error("two PKCE invocations produced identical verifiers (entropy failure)")
	}
}

func TestRandomState_NonEmpty(t *testing.T) {
	s, err := RandomState()
	if err != nil {
		t.Fatalf("RandomState: %v", err)
	}
	if s == "" {
		t.Error("RandomState returned empty string")
	}
}
