package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	DefaultServerURL = "https://searchlight.io"
	UserAgent        = "searchlight-cli"

	envServerURL = "SEARCHLIGHT_URL"
	envClientID  = "SEARCHLIGHT_CLIENT_ID"
)

// ClientIDs are baked at build time via -ldflags="-X ...prodClientID=..." after
// an admin creates the OauthApplication rows on staging and prod Rails consoles.
// See CLAUDE.md, "OAuth client setup (one-time, manual)".
var (
	prodClientID    = ""
	stagingClientID = ""
)

type Config struct {
	ServerURL string
	ClientID  string
	CacheDir  string
	ConfigDir string
}

func Load() (*Config, error) {
	server := strings.TrimRight(getenv(envServerURL, DefaultServerURL), "/")

	clientID := os.Getenv(envClientID)
	if clientID == "" {
		switch {
		case strings.Contains(server, "staging") || strings.Contains(server, "localhost"):
			clientID = stagingClientID
		default:
			clientID = prodClientID
		}
	}
	if clientID == "" {
		return nil, fmt.Errorf(
			"no OAuth client_id configured for %s; set %s or rebuild with -ldflags",
			server, envClientID,
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
