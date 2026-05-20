package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/headlinevc/searchlight-cli/internal/mcp"
	"github.com/headlinevc/searchlight-cli/internal/output"
)

func newToolsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "tools",
		Short: "List, describe, and refresh available MCP tools",
		RunE: func(cmd *cobra.Command, args []string) error {
			tools, err := loadTools(false)
			if err != nil {
				return err
			}
			return output.WriteValue(os.Stdout, map[string]any{
				"count": len(tools),
				"tools": tools,
			}, globals.Pretty)
		},
	}
	c.AddCommand(newToolsRefreshCmd(), newToolsDescribeCmd())
	return c
}

func newToolsRefreshCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Force re-fetch of tools/list from the server",
		RunE: func(cmd *cobra.Command, args []string) error {
			tools, err := loadTools(true)
			if err != nil {
				return err
			}
			output.HumanF(globals.Quiet, "Cached %d tools.", len(tools))
			return output.WriteValue(os.Stdout, map[string]any{"count": len(tools)}, globals.Pretty)
		},
	}
}

func newToolsDescribeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "describe <tool-name>",
		Short: "Print one tool's schema",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tools, err := loadTools(false)
			if err != nil {
				return err
			}
			for _, t := range tools {
				if t.Name == args[0] {
					return output.WriteValue(os.Stdout, t, globals.Pretty)
				}
			}
			return fmt.Errorf("tool not found: %s", args[0])
		},
	}
}

func loadTools(force bool) ([]mcp.ToolDefinition, error) {
	if globals.Schema.Path == "" {
		return nil, fmt.Errorf("schema cache not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return globals.Schema.LoadOrFetch(ctx, globals.MCP, force || globals.NoCache)
}

func writeToolResult(res *mcp.ToolCallResult) error {
	if len(res.Content) == 0 {
		return output.WriteValue(os.Stdout, map[string]any{"empty": true}, globals.Pretty)
	}
	first := res.Content[0]
	// Tool content is typed text but the convention is that the text payload
	// is itself JSON. If it parses cleanly, pass it through; otherwise wrap.
	if json.Valid([]byte(first.Text)) {
		err := output.WriteJSON(os.Stdout, json.RawMessage(first.Text), globals.Pretty)
		if err != nil {
			return err
		}
	} else {
		_ = output.WriteValue(os.Stdout, map[string]string{"text": first.Text}, globals.Pretty)
	}
	if res.IsError {
		return fmt.Errorf("tool returned isError=true")
	}
	return nil
}
