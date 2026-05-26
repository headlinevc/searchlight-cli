package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestLogin_DiscoverMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/oauth-authorization-server" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(Metadata{
			Issuer:                "https://example.com",
			AuthorizationEndpoint: "https://example.com/authorize",
			TokenEndpoint:         "https://example.com/oauth/token",
			GrantTypesSupported:   []string{"authorization_code", "refresh_token"},
			CodeChallengeMethods:  []string{"S256"},
			ScopesSupported:       []string{"read"},
		})
	}))
	defer srv.Close()

	l := Login{ServerURL: srv.URL, HTTPClient: srv.Client()}
	md, err := l.DiscoverMetadata(context.Background())
	if err != nil {
		t.Fatalf("DiscoverMetadata: %v", err)
	}
	if md.AuthorizationEndpoint != "https://example.com/authorize" {
		t.Errorf("authorization_endpoint = %q", md.AuthorizationEndpoint)
	}
	if md.TokenEndpoint != "https://example.com/oauth/token" {
		t.Errorf("token_endpoint = %q", md.TokenEndpoint)
	}
}

func TestLogin_DiscoverMetadata_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no", http.StatusInternalServerError)
	}))
	defer srv.Close()
	l := Login{ServerURL: srv.URL, HTTPClient: srv.Client()}
	if _, err := l.DiscoverMetadata(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestBuildAuthorizeURL_AllParams(t *testing.T) {
	pkce := &PKCE{Verifier: "v", Challenge: "ch", Method: "S256"}
	got := buildAuthorizeURL("https://example.com/authorize",
		"client-id", "http://127.0.0.1:5000/callback", "scope1", "state-xyz", pkce)

	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	q := parsed.Query()
	expected := map[string]string{
		"client_id":             "client-id",
		"redirect_uri":          "http://127.0.0.1:5000/callback",
		"response_type":         "code",
		"code_challenge":        "ch",
		"code_challenge_method": "S256",
		"state":                 "state-xyz",
		"scope":                 "scope1",
	}
	for k, want := range expected {
		if got := q.Get(k); got != want {
			t.Errorf("query %q = %q, want %q", k, got, want)
		}
	}
}

func TestBuildAuthorizeURL_DefaultScope(t *testing.T) {
	pkce := &PKCE{Verifier: "v", Challenge: "ch", Method: "S256"}
	got := buildAuthorizeURL("https://example.com/authorize",
		"client-id", "http://127.0.0.1:5000/callback", "", "state-xyz", pkce)
	if !strings.Contains(got, "scope=read") {
		t.Errorf("default scope missing: %s", got)
	}
}

func TestBuildAuthorizeURL_PreservesExistingQuery(t *testing.T) {
	pkce := &PKCE{Verifier: "v", Challenge: "ch", Method: "S256"}
	got := buildAuthorizeURL("https://example.com/authorize?foo=bar",
		"client-id", "http://127.0.0.1:5000/callback", "read", "state-xyz", pkce)
	// existing query should remain
	if !strings.Contains(got, "foo=bar") {
		t.Errorf("existing query lost: %s", got)
	}
	// and new params should be appended with &, not ?
	if strings.Count(got, "?") != 1 {
		t.Errorf("unexpected ? count in %s", got)
	}
}
