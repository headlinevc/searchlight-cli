package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/pkg/browser"
)

const (
	loopbackHost    = "127.0.0.1"
	wellKnownPath   = "/.well-known/oauth-authorization-server"
	defaultTimeout  = 5 * time.Minute
	successResponse = `<!doctype html><meta charset="utf-8"><title>searchlight</title>
<style>body{font-family:system-ui;max-width:32rem;margin:4rem auto;padding:0 1rem;color:#222}</style>
<h1>You're signed in.</h1>
<p>You can close this window and return to the terminal.</p>`
)

type Metadata struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	GrantTypesSupported   []string `json:"grant_types_supported"`
	ResponseTypesSupp     []string `json:"response_types_supported"`
	CodeChallengeMethods  []string `json:"code_challenge_methods_supported"`
	ScopesSupported       []string `json:"scopes_supported"`
}

type Login struct {
	ServerURL  string
	ClientID   string
	HTTPClient *http.Client
}

type LoginResult struct {
	Tokens   *Tokens
	Metadata *Metadata
}

func (l Login) httpClient() *http.Client {
	if l.HTTPClient != nil {
		return l.HTTPClient
	}
	return http.DefaultClient
}

// DiscoverMetadata pulls the RFC 8414 server metadata. We trust the server URL the
// user is pointing at — if you wanted strict RFC 9728 protected-resource discovery,
// you'd hit /.well-known/oauth-protected-resource on the /mcp resource first.
func (l Login) DiscoverMetadata(ctx context.Context) (*Metadata, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.ServerURL+wellKnownPath, nil)
	if err != nil {
		return nil, err
	}
	resp, err := l.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch oauth metadata: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oauth metadata: %s", resp.Status)
	}
	var md Metadata
	if err := json.NewDecoder(resp.Body).Decode(&md); err != nil {
		return nil, fmt.Errorf("decode oauth metadata: %w", err)
	}
	return &md, nil
}

// Run executes the full PKCE authorization_code dance with a loopback redirect.
// Blocks until the user completes the browser flow or the timeout fires.
func (l Login) Run(ctx context.Context, scope string) (*LoginResult, error) {
	md, err := l.DiscoverMetadata(ctx)
	if err != nil {
		return nil, err
	}

	pkce, err := NewPKCE()
	if err != nil {
		return nil, err
	}
	state, err := RandomState()
	if err != nil {
		return nil, err
	}

	listener, err := net.Listen("tcp", loopbackHost+":0")
	if err != nil {
		return nil, fmt.Errorf("bind loopback: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://%s:%d/callback", loopbackHost, port)

	authURL := buildAuthorizeURL(md.AuthorizationEndpoint, l.ClientID, redirectURI, scope, state, pkce)

	type callbackResult struct {
		code string
		err  error
	}
	resultCh := make(chan callbackResult, 1)

	server := &http.Server{
		Handler:           http.NewServeMux(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	mux := server.Handler.(*http.ServeMux)
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if errParam := q.Get("error"); errParam != "" {
			msg := fmt.Sprintf("OAuth error: %s — %s", errParam, q.Get("error_description"))
			http.Error(w, msg, http.StatusBadRequest)
			resultCh <- callbackResult{err: fmt.Errorf(msg)}
			return
		}
		if got := q.Get("state"); got != state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			resultCh <- callbackResult{err: fmt.Errorf("oauth: state mismatch")}
			return
		}
		code := q.Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			resultCh <- callbackResult{err: fmt.Errorf("oauth: missing code")}
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(successResponse))
		resultCh <- callbackResult{code: code}
	})

	go func() { _ = server.Serve(listener) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	// Always print the URL — some environments (WSL, headless containers, SSH
	// sessions) make browser.OpenURL silently succeed without actually opening
	// anything, so relying on its error return to decide whether to print the
	// URL leaves the user stuck staring at a blank prompt.
	fmt.Fprintf(os.Stderr, "Open this URL in your browser to sign in:\n\n  %s\n\n", authURL)
	if err := browser.OpenURL(authURL); err != nil {
		fmt.Fprintf(os.Stderr, "(browser did not auto-open: %v — paste the URL above manually)\n\n", err)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	var code string
	select {
	case <-timeoutCtx.Done():
		return nil, fmt.Errorf("oauth: timeout waiting for browser callback")
	case res := <-resultCh:
		if res.err != nil {
			return nil, res.err
		}
		code = res.code
	}

	tokens, err := ExchangeCode(ctx, l.httpClient(), md.TokenEndpoint, l.ClientID, code, redirectURI, pkce.Verifier)
	if err != nil {
		return nil, err
	}
	return &LoginResult{Tokens: tokens, Metadata: md}, nil
}

func buildAuthorizeURL(endpoint, clientID, redirectURI, scope, state string, pkce *PKCE) string {
	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("code_challenge", pkce.Challenge)
	q.Set("code_challenge_method", pkce.Method)
	q.Set("state", state)
	if scope == "" {
		scope = "read"
	}
	q.Set("scope", scope)

	sep := "?"
	if strings.Contains(endpoint, "?") {
		sep = "&"
	}
	return endpoint + sep + q.Encode()
}
