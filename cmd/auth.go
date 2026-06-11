package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/headlinevc/searchlight-cli/internal/oauth"
	"github.com/headlinevc/searchlight-cli/internal/output"
)

func newAuthCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "auth",
		Short: "Manage Searchlight CLI authentication",
	}
	c.AddCommand(newAuthLoginCmd(), newAuthLogoutCmd(), newAuthRefreshCmd(), newAuthWhoamiCmd())
	return c
}

func newAuthLoginCmd() *cobra.Command {
	var scope string
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in via OAuth (PKCE + browser loopback)",
		RunE: func(_ *cobra.Command, _ []string) error {
			if globals.Cfg == nil {
				return fmt.Errorf("config not initialized")
			}
			output.HumanF(globals.Quiet, "Opening browser to %s …", globals.Cfg.ServerURL)
			flow := oauth.Login{
				ServerURL:  globals.Cfg.ServerURL,
				ClientID:   globals.Cfg.ClientID,
				HTTPClient: globals.HTTPC,
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			res, err := flow.Run(ctx, scope)
			if err != nil {
				return err
			}
			if err := (oauth.KeyringStore{Backend: newKeyringBackend()}).Save(res.Tokens); err != nil {
				return fmt.Errorf("save credentials: %w", err)
			}
			globals.Tokens.ForceRefresh()
			refreshSchemaAfterLogin()
			output.HumanF(globals.Quiet, "Signed in. Token expires in %ds.", res.Tokens.ExpiresIn)
			return output.WriteValue(os.Stdout, map[string]any{
				"status":     "ok",
				"expires_in": res.Tokens.ExpiresIn,
			}, globals.Pretty)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "read", "OAuth scope to request")
	return cmd
}

// refreshSchemaAfterLogin best-effort populates the tools cache right after a
// successful login, so a fresh sign-in leaves the user with working dynamic
// commands in one step. Failure only warns — the tokens are already saved, so
// auth itself succeeded and login must still exit 0.
//
// The fetch goes through schemaRefreshClient rather than globals.MCP: the
// cache must be fetched as the principal that will execute future tool calls
// — the env token when SEARCHLIGHT_TOKEN is set (tool availability can differ
// per principal), otherwise the OAuth tokens just saved — and a revoked env
// token must fail into the warning below rather than re-triggering the
// interactive fallback; the user's next real call hits that fallback where it
// belongs.
func refreshSchemaAfterLogin() {
	if globals.Schema.Path == "" {
		output.HumanF(globals.Quiet, "warning: could not cache tool schemas (schema cache not initialized); run `searchlight tools refresh` to retry")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tools, err := globals.Schema.LoadOrFetch(ctx, schemaRefreshClient(), true)
	if err != nil {
		output.HumanF(globals.Quiet, "warning: could not cache tool schemas (%v); run `searchlight tools refresh` to retry", err)
		return
	}
	output.HumanF(globals.Quiet, "Cached %d tools.", len(tools))
}

func newAuthLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Delete stored credentials",
		RunE: func(_ *cobra.Command, _ []string) error {
			store := oauth.KeyringStore{Backend: newKeyringBackend()}
			if err := store.Clear(); err != nil {
				return err
			}
			return output.WriteValue(os.Stdout, map[string]string{"status": "ok"}, globals.Pretty)
		},
	}
}

func newAuthRefreshCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Force a refresh-token exchange",
		RunE: func(_ *cobra.Command, _ []string) error {
			globals.Tokens.ForceRefresh()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if _, err := globals.Tokens.AccessToken(ctx); err != nil {
				return err
			}
			return output.WriteValue(os.Stdout, map[string]string{"status": "ok"}, globals.Pretty)
		},
	}
}

func newAuthWhoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Identify the currently signed-in user (calls get_current_user)",
		RunE: func(_ *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			res, err := globals.MCP.CallTool(ctx, "get_current_user", map[string]any{})
			if err != nil {
				return err
			}
			return writeToolResult(res)
		},
	}
}
