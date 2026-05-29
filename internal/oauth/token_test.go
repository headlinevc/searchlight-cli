package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func okTokenHandler(t *testing.T, want map[string]string, resp Tokens) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
			t.Errorf("Content-Type = %q", ct)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		for k, v := range want {
			if got := r.PostForm.Get(k); got != v {
				t.Errorf("form[%q] = %q, want %q", k, got, v)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func TestExchangeCode_Success(t *testing.T) {
	srv := httptest.NewServer(okTokenHandler(t,
		map[string]string{
			"grant_type":    "authorization_code",
			"code":          "abc",
			"redirect_uri":  "http://127.0.0.1:1234/callback",
			"client_id":     "cli",
			"code_verifier": "ver",
		},
		Tokens{AccessToken: "at", RefreshToken: "rt", TokenType: "Bearer", ExpiresIn: 900},
	))
	defer srv.Close()

	tok, err := ExchangeCode(context.Background(), srv.Client(), srv.URL, "cli", "abc",
		"http://127.0.0.1:1234/callback", "ver")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if tok.AccessToken != "at" || tok.RefreshToken != "rt" {
		t.Errorf("tokens = %+v", tok)
	}
	if tok.ObtainedAt.IsZero() {
		t.Error("ObtainedAt should be set")
	}
}

func TestExchangeCode_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
	}))
	defer srv.Close()

	_, err := ExchangeCode(context.Background(), srv.Client(), srv.URL, "cli", "x", "y", "z")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error missing status: %v", err)
	}
}

func TestExchangeCode_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	_, err := ExchangeCode(context.Background(), srv.Client(), srv.URL, "cli", "x", "y", "z")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRefreshTokens_Success(t *testing.T) {
	srv := httptest.NewServer(okTokenHandler(t,
		map[string]string{
			"grant_type":    "refresh_token",
			"refresh_token": "old-rt",
			"client_id":     "cli",
		},
		Tokens{AccessToken: "new-at", RefreshToken: "new-rt", TokenType: "Bearer", ExpiresIn: 900},
	))
	defer srv.Close()

	tok, err := RefreshTokens(context.Background(), srv.Client(), srv.URL, "cli", "old-rt")
	if err != nil {
		t.Fatalf("RefreshTokens: %v", err)
	}
	if tok.AccessToken != "new-at" {
		t.Errorf("AccessToken = %q", tok.AccessToken)
	}
}

func TestTokens_ExpiresAtAndIsExpired(t *testing.T) {
	t.Run("zero ObtainedAt → never expires", func(t *testing.T) {
		tok := &Tokens{ExpiresIn: 60}
		if !tok.ExpiresAt().IsZero() {
			t.Error("ExpiresAt should be zero when ObtainedAt is zero")
		}
		if tok.IsExpired(0) {
			t.Error("IsExpired should be false when ObtainedAt is zero")
		}
	})
	t.Run("past expiry", func(t *testing.T) {
		tok := &Tokens{ObtainedAt: time.Now().Add(-2 * time.Hour), ExpiresIn: 60}
		if !tok.IsExpired(0) {
			t.Error("expected expired")
		}
	})
	t.Run("future expiry", func(t *testing.T) {
		tok := &Tokens{ObtainedAt: time.Now(), ExpiresIn: 3600}
		if tok.IsExpired(0) {
			t.Error("expected not expired")
		}
	})
	t.Run("expires within skew → counted as expired", func(t *testing.T) {
		tok := &Tokens{ObtainedAt: time.Now().Add(-59 * time.Second), ExpiresIn: 60}
		if !tok.IsExpired(5 * time.Second) {
			t.Error("expected expired within skew window")
		}
	})
}

// memStore is a simple in-memory TokenStore for Manager tests.
type memStore struct {
	mu     sync.Mutex
	tokens *Tokens
}

func (m *memStore) Load() (*Tokens, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tokens == nil {
		return nil, nil
	}
	cp := *m.tokens
	return &cp, nil
}
func (m *memStore) Save(t *Tokens) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *t
	m.tokens = &cp
	return nil
}
func (m *memStore) Clear() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens = nil
	return nil
}

func TestManager_AccessToken_NoCredentials(t *testing.T) {
	m := NewManager(&memStore{}, "http://nope", "cli", nil)
	_, err := m.AccessToken(context.Background())
	if err == nil {
		t.Fatal("expected error for missing credentials")
	}
}

func TestManager_AccessToken_FreshToken(t *testing.T) {
	store := &memStore{tokens: &Tokens{
		AccessToken: "fresh", RefreshToken: "rt",
		ObtainedAt: time.Now(), ExpiresIn: 3600,
	}}
	m := NewManager(store, "http://nope", "cli", nil)
	tok, err := m.AccessToken(context.Background())
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if tok != "fresh" {
		t.Errorf("token = %q, want fresh", tok)
	}
}

func TestManager_AccessToken_RefreshesWhenExpired(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_ = json.NewEncoder(w).Encode(Tokens{
			AccessToken: "refreshed", RefreshToken: "new-rt", TokenType: "Bearer", ExpiresIn: 3600,
		})
	}))
	defer srv.Close()

	store := &memStore{tokens: &Tokens{
		AccessToken: "old", RefreshToken: "rt",
		ObtainedAt: time.Now().Add(-2 * time.Hour), ExpiresIn: 60, // expired
	}}
	m := NewManager(store, srv.URL, "cli", srv.Client())

	tok, err := m.AccessToken(context.Background())
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if tok != "refreshed" {
		t.Errorf("token = %q, want refreshed", tok)
	}
	if hits != 1 {
		t.Errorf("server hits = %d, want 1", hits)
	}
	// And it should have persisted the new tokens.
	saved, _ := store.Load()
	if saved.AccessToken != "refreshed" || saved.RefreshToken != "new-rt" {
		t.Errorf("persisted tokens = %+v", saved)
	}
}

func TestManager_AccessToken_RefreshKeepsOldRTWhenServerOmits(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(Tokens{
			AccessToken: "refreshed", TokenType: "Bearer", ExpiresIn: 3600,
		})
	}))
	defer srv.Close()

	store := &memStore{tokens: &Tokens{
		AccessToken: "old", RefreshToken: "preserved-rt",
		ObtainedAt: time.Now().Add(-2 * time.Hour), ExpiresIn: 60,
	}}
	m := NewManager(store, srv.URL, "cli", srv.Client())
	if _, err := m.AccessToken(context.Background()); err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	saved, _ := store.Load()
	if saved.RefreshToken != "preserved-rt" {
		t.Errorf("RefreshToken = %q, want preserved", saved.RefreshToken)
	}
}

func TestManager_AccessToken_NoRefreshTokenWhenExpired(t *testing.T) {
	store := &memStore{tokens: &Tokens{
		AccessToken: "old", // no RefreshToken
		ObtainedAt: time.Now().Add(-2 * time.Hour), ExpiresIn: 60,
	}}
	m := NewManager(store, "http://nope", "cli", nil)
	_, err := m.AccessToken(context.Background())
	if err == nil {
		t.Fatal("expected error when expired and no refresh token")
	}
	if !strings.Contains(err.Error(), "refresh") {
		t.Errorf("error %q missing 'refresh'", err)
	}
}

func TestManager_ForceRefresh_ReloadsFromStore(t *testing.T) {
	store := &memStore{tokens: &Tokens{
		AccessToken: "v1", RefreshToken: "rt",
		ObtainedAt: time.Now(), ExpiresIn: 3600,
	}}
	m := NewManager(store, "http://nope", "cli", nil)
	if tok, _ := m.AccessToken(context.Background()); tok != "v1" {
		t.Errorf("v1 → %q", tok)
	}
	// Mutate the store out-of-band.
	_ = store.Save(&Tokens{
		AccessToken: "v2", RefreshToken: "rt",
		ObtainedAt: time.Now(), ExpiresIn: 3600,
	})
	// Without ForceRefresh, the manager still returns v1 from its cache.
	if tok, _ := m.AccessToken(context.Background()); tok != "v1" {
		t.Errorf("cached token = %q, want v1", tok)
	}
	m.ForceRefresh()
	if tok, _ := m.AccessToken(context.Background()); tok != "v2" {
		t.Errorf("after force refresh = %q, want v2", tok)
	}
}

func TestFallbackTokenSource_ReturnsToken(t *testing.T) {
	tok, err := NewFallbackTokenSource("mcp-token-xyz", false, nil, nil, nil).AccessToken(context.Background())
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if tok != "mcp-token-xyz" {
		t.Errorf("token = %q, want mcp-token-xyz", tok)
	}
}

func TestFallbackTokenSource_EmptyErrors(t *testing.T) {
	if _, err := NewFallbackTokenSource("", false, nil, nil, nil).AccessToken(context.Background()); err == nil {
		t.Fatal("expected error for empty token")
	}
}

func TestFallbackTokenSource_NonInteractive_WarnsAndKeepsToken(t *testing.T) {
	var warnings []string
	loginCalled := false
	src := NewFallbackTokenSource("revoked", false,
		func(m string) { warnings = append(warnings, m) },
		func(context.Context) (*Tokens, error) { loginCalled = true; return &Tokens{AccessToken: "new"}, nil },
		nil,
	)
	src.ForceRefresh()
	if loginCalled {
		t.Error("login must not run in a non-interactive context (would hang CI)")
	}
	if tok, _ := src.AccessToken(context.Background()); tok != "revoked" {
		t.Errorf("token = %q, want unchanged 'revoked'", tok)
	}
	if len(warnings) == 0 {
		t.Error("expected a warning that the token was rejected")
	}
}

func TestFallbackTokenSource_Interactive_FallsBackToLogin(t *testing.T) {
	var persisted *Tokens
	src := NewFallbackTokenSource("revoked", true, nil,
		func(context.Context) (*Tokens, error) { return &Tokens{AccessToken: "fresh"}, nil },
		func(tok *Tokens) { persisted = tok },
	)
	src.ForceRefresh()
	if tok, _ := src.AccessToken(context.Background()); tok != "fresh" {
		t.Errorf("token = %q, want 'fresh' after browser fallback", tok)
	}
	if persisted == nil || persisted.AccessToken != "fresh" {
		t.Errorf("expected the fresh token persisted, got %+v", persisted)
	}
}

func TestFallbackTokenSource_Interactive_LoginErrorKeepsToken(t *testing.T) {
	src := NewFallbackTokenSource("revoked", true, nil,
		func(context.Context) (*Tokens, error) { return nil, fmt.Errorf("user cancelled") },
		nil,
	)
	src.ForceRefresh()
	if tok, _ := src.AccessToken(context.Background()); tok != "revoked" {
		t.Errorf("token = %q, want unchanged when login fails", tok)
	}
}

func TestFallbackTokenSource_OnlyTriesLoginOnce(t *testing.T) {
	calls := 0
	src := NewFallbackTokenSource("revoked", true, nil,
		func(context.Context) (*Tokens, error) { calls++; return nil, fmt.Errorf("nope") },
		nil,
	)
	src.ForceRefresh()
	src.ForceRefresh()
	if calls != 1 {
		t.Errorf("login attempts = %d, want 1", calls)
	}
}

func TestManager_AccessToken_StoreLoadError(t *testing.T) {
	m := NewManager(errStore{}, "http://nope", "cli", nil)
	_, err := m.AccessToken(context.Background())
	if !errors.Is(err, errStoreErr) {
		t.Errorf("err = %v, want %v", err, errStoreErr)
	}
}

var errStoreErr = errors.New("simulated store error")

type errStore struct{}

func (errStore) Load() (*Tokens, error) { return nil, errStoreErr }
func (errStore) Save(*Tokens) error     { return errStoreErr }
func (errStore) Clear() error           { return errStoreErr }
