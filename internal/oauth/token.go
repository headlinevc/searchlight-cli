package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Tokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type"`
	ExpiresIn    int       `json:"expires_in"`
	Scope        string    `json:"scope,omitempty"`
	ObtainedAt   time.Time `json:"obtained_at"`
}

func (t *Tokens) ExpiresAt() time.Time {
	if t.ObtainedAt.IsZero() {
		return time.Time{}
	}
	return t.ObtainedAt.Add(time.Duration(t.ExpiresIn) * time.Second)
}

func (t *Tokens) IsExpired(skew time.Duration) bool {
	exp := t.ExpiresAt()
	if exp.IsZero() {
		return false
	}
	return time.Now().Add(skew).After(exp)
}

func ExchangeCode(ctx context.Context, client *http.Client, tokenURL, clientID, code, redirectURI, verifier string) (*Tokens, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", clientID)
	form.Set("code_verifier", verifier)
	return postTokenForm(ctx, client, tokenURL, form)
}

func RefreshTokens(ctx context.Context, client *http.Client, tokenURL, clientID, refreshToken string) (*Tokens, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", clientID)
	return postTokenForm(ctx, client, tokenURL, form)
}

func postTokenForm(ctx context.Context, client *http.Client, tokenURL string, form url.Values) (*Tokens, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth token exchange: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oauth token exchange: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var t Tokens
	if err := json.Unmarshal(body, &t); err != nil {
		return nil, fmt.Errorf("oauth token response: %w", err)
	}
	t.ObtainedAt = time.Now()
	return &t, nil
}

// Manager is a goroutine-safe holder that lazy-loads tokens from the store,
// refreshes them transparently when they're close to expiry, and writes the
// refreshed credentials back.
type Manager struct {
	store        TokenStore
	tokenURL     string
	clientID     string
	httpClient   *http.Client
	clockSkew    time.Duration

	mu     sync.Mutex
	cached *Tokens
}

type TokenStore interface {
	Load() (*Tokens, error)
	Save(*Tokens) error
	Clear() error
}

func NewManager(store TokenStore, tokenURL, clientID string, client *http.Client) *Manager {
	if client == nil {
		client = http.DefaultClient
	}
	return &Manager{
		store:      store,
		tokenURL:   tokenURL,
		clientID:   clientID,
		httpClient: client,
		clockSkew:  30 * time.Second,
	}
}

// AccessToken returns a non-expired access token, refreshing if needed.
func (m *Manager) AccessToken(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cached == nil {
		t, err := m.store.Load()
		if err != nil {
			return "", err
		}
		m.cached = t
	}
	if m.cached == nil || m.cached.AccessToken == "" {
		return "", fmt.Errorf("no credentials; run `searchlight auth login`")
	}
	if !m.cached.IsExpired(m.clockSkew) {
		return m.cached.AccessToken, nil
	}
	if m.cached.RefreshToken == "" {
		return "", fmt.Errorf("access token expired and no refresh token; run `searchlight auth login`")
	}
	fresh, err := RefreshTokens(ctx, m.httpClient, m.tokenURL, m.clientID, m.cached.RefreshToken)
	if err != nil {
		return "", err
	}
	if fresh.RefreshToken == "" {
		// Some servers omit refresh_token on refresh; keep the old one.
		fresh.RefreshToken = m.cached.RefreshToken
	}
	if err := m.store.Save(fresh); err != nil {
		return "", err
	}
	m.cached = fresh
	return fresh.AccessToken, nil
}

// ForceRefresh invalidates the in-memory cache so the next AccessToken call
// re-reads from the store. Use this after a 401 to handle a token rotated
// out-of-band by another process.
func (m *Manager) ForceRefresh() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cached = nil
}

// FallbackTokenSource serves a pre-minted MCP token (from SEARCHLIGHT_TOKEN),
// bypassing the OAuth flow and keyring on the happy path. MCP tokens don't
// expire, so the only failure is revocation: when the server rejects the token
// with a 401 the MCP client calls ForceRefresh, and we react based on context.
//
// On an interactive terminal we warn and run the browser login flow once, then
// serve the resulting token so the request can be retried. In non-interactive
// contexts (CI) we only warn and leave the token unchanged — opening a browser
// there would block on a callback that never arrives — so the 401 surfaces as
// permission_denied and the run fails fast.
type FallbackTokenSource struct {
	mu          sync.Mutex
	token       string
	tried       bool
	interactive bool
	warn        func(string)
	login       func(context.Context) (*Tokens, error) // nil when login isn't possible (e.g. no client_id)
	persist     func(*Tokens)                           // optional; saves a successful fallback login
}

func NewFallbackTokenSource(token string, interactive bool, warn func(string), login func(context.Context) (*Tokens, error), persist func(*Tokens)) *FallbackTokenSource {
	if warn == nil {
		warn = func(string) {}
	}
	return &FallbackTokenSource{
		token:       token,
		interactive: interactive,
		warn:        warn,
		login:       login,
		persist:     persist,
	}
}

func (f *FallbackTokenSource) AccessToken(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.token == "" {
		return "", fmt.Errorf("empty token")
	}
	return f.token, nil
}

func (f *FallbackTokenSource) ForceRefresh() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.tried {
		return
	}
	f.tried = true
	f.warn("SEARCHLIGHT_TOKEN was rejected (invalid or revoked)")
	if !f.interactive || f.login == nil {
		return
	}
	f.warn("falling back to browser login; replace or unset SEARCHLIGHT_TOKEN to skip this next time")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	toks, err := f.login(ctx)
	if err != nil {
		f.warn("browser login failed: " + err.Error())
		return
	}
	f.token = toks.AccessToken
	if f.persist != nil {
		f.persist(toks)
	}
}
