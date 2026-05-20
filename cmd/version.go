package cmd

import (
	"context"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/headlinevc/searchlight-cli/internal/output"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print CLI version, server version, and schema cache age",
		RunE: func(_ *cobra.Command, _ []string) error {
			info := map[string]any{
				"cli": map[string]string{
					"version":    versionInfo.Version,
					"commit":     versionInfo.Commit,
					"build_date": versionInfo.BuildDate,
				},
			}
			if globals.MCP != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if init, err := globals.MCP.Initialize(ctx); err == nil {
					info["server"] = map[string]string{
						"name":    init.ServerInfo.Name,
						"version": init.ServerInfo.Version,
					}
				}
			}
			if globals.Cfg != nil {
				info["config"] = map[string]string{
					"server_url": globals.Cfg.ServerURL,
					"cache_dir":  globals.Cfg.CacheDir,
				}
				if cs, err := globals.Schema.Load(); err == nil && cs != nil {
					info["schema_cache"] = map[string]any{
						"server_version": cs.ServerVersion,
						"obtained_at":    cs.ObtainedAt,
						"tool_count":     len(cs.Tools),
						"age_seconds":    int(time.Since(cs.ObtainedAt).Seconds()),
					}
				}
			}
			return output.WriteValue(os.Stdout, info, globals.Pretty)
		},
	}
}
