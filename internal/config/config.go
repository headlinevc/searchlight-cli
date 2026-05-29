package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	DefaultServerURL = "https://searchlight.headline.com"
	UserAgent        = "searchlight-cli"

	envServerURL = "SEARCHLIGHT_URL"
	envClientID  = "SEARCHLIGHT_CLIENT_ID"
	envToken     = "SEARCHLIGHT_TOKEN"
)

// ClientID is baked at build time via -ldflags="-X ...prodClientID=..." after
// an admin registers the OAuth application on the server.
// See CLAUDE.md, "OAuth client setup (one-time, manual)".
var prodClientID = ""

type Config struct {
	ServerURL string
	ClientID  string
	Token     string
	CacheDir  string
	ConfigDir string
}

func Load() (*Config, error) {
	server := strings.TrimRight(getenv(envServerURL, DefaultServerURL), "/")

	// A pre-minted MCP token authenticates non-interactively (CI, GitHub
	// Actions) by being sent as the Bearer directly, bypassing the OAuth flow.
	token := os.Getenv(envToken)

	clientID := os.Getenv(envClientID)
	if clientID == "" {
		clientID = prodClientID
	}
	// client_id is only needed for the interactive OAuth flow; with a token it
	// is irrelevant, so don't block startup on it.
	if clientID == "" && token == "" {
		return nil, fmt.Errorf(
			"no OAuth client_id configured for %s; set %s, provide %s, or rebuild with -ldflags",
			server, envClientID, envToken,
		)
	}

	cache, err := userCacheDir()
	if err != nil {
		return nil, err
	}
	cfg, err := userConfigDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cache, 0o700); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}
	if err := os.MkdirAll(cfg, 0o700); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}

	return &Config{
		ServerURL: server,
		ClientID:  clientID,
		Token:     token,
		CacheDir:  cache,
		ConfigDir: cfg,
	}, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func userCacheDir() (string, error) {
	root, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "searchlight"), nil
}

func userConfigDir() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "searchlight"), nil
}

// ToolsCachePath returns the version-keyed path for tools/list cache.
// Embedding the server version in the filename gives us free cache invalidation
// when the MCP server bumps its tools schema.
func (c *Config) ToolsCachePath(serverVersion string) string {
	if serverVersion == "" {
		serverVersion = "unknown"
	}
	return filepath.Join(c.CacheDir, fmt.Sprintf("tools-%s.json", serverVersion))
}

func (c *Config) CredentialsPath() string {
	return filepath.Join(c.ConfigDir, "credentials.json")
}
