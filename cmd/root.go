package cmd

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/headlinevc/searchlight-cli/internal/config"
	"github.com/headlinevc/searchlight-cli/internal/keyring"
	"github.com/headlinevc/searchlight-cli/internal/mcp"
	"github.com/headlinevc/searchlight-cli/internal/oauth"
)

type Globals struct {
	Pretty   bool
	Quiet    bool
	NoCache  bool
	Cfg      *config.Config
	HTTPC    *http.Client
	Tokens   *oauth.Manager
	MCP      *mcp.Client
	Schema   mcp.SchemaCache
}

var (
	globals      = &Globals{HTTPC: &http.Client{Timeout: 60 * time.Second}}
	versionInfo  = struct {
		Version   string
		Commit    string
		BuildDate string
	}{"dev", "none", "unknown"}
)

func SetBuildInfo(v, c, d string) {
	versionInfo.Version = v
	versionInfo.Commit = c
	versionInfo.BuildDate = d
}

func newRootCmd() *cobra.Command {
	rc := &cobra.Command{
		Use:               "searchlight",
		Short:             "Searchlight CLI — a thin client over the Searchlight MCP server",
		SilenceUsage:      true,
		SilenceErrors:     true,
		DisableAutoGenTag: true,
	}

	rc.PersistentFlags().BoolVar(&globals.Pretty, "pretty", false, "pretty-print JSON output")
	rc.PersistentFlags().BoolVarP(&globals.Quiet, "quiet", "q", false, "suppress stderr progress/hints")
	rc.PersistentFlags().BoolVar(&globals.NoCache, "no-cache", false, "force refresh of tools/list cache")

	rc.AddCommand(
		newAuthCmd(),
		newToolsCmd(),
		newVersionCmd(),
	)
	return rc
}

// Execute is the entry point called from main.
//
// Order:
//  1. Build cobra tree with the static commands.
//  2. Attempt to load config + tokens + schema cache.
//  3. Register dynamic tool subcommands from the cache (best-effort — if no
//     cache yet, top-level help still works; the user just needs to run
//     `searchlight auth login` then `searchlight tools refresh`).
//  4. cobra parses argv and dispatches.
func Execute() error {
	root := newRootCmd()

	if err := setupGlobals(); err != nil {
		// Config errors shouldn't prevent --help from working. Surface them
		// later when a command actually needs the config.
		root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error { return err }
	} else if globals.Schema.Path != "" {
		registerDynamicTools(root)
	}

	return root.Execute()
}

func setupGlobals() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	globals.Cfg = cfg

	kr := keyring.New(cfg.CredentialsPath())
	store := oauth.KeyringStore{Backend: kr}

	// We don't know the token endpoint until we've discovered metadata,
	// but oauth.Manager is happy without it for the initial Load — refresh
	// will only fire if AccessToken is called and tokens are expired.
	tokenEndpoint := strings.TrimRight(cfg.ServerURL, "/") + "/oauth/token"
	globals.Tokens = oauth.NewManager(store, tokenEndpoint, cfg.ClientID, globals.HTTPC)

	globals.MCP = &mcp.Client{
		ServerURL:  cfg.ServerURL,
		UserAgent:  fmt.Sprintf("searchlight-cli/%s", versionInfo.Version),
		HTTPClient: globals.HTTPC,
		Tokens:     globals.Tokens,
	}
	globals.Schema = mcp.SchemaCache{
		Path: cfg.ToolsCachePath(""),
		TTL:  24 * time.Hour,
	}
	return nil
}

func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}
