package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/headlinevc/searchlight-cli/internal/mcp"
	"github.com/headlinevc/searchlight-cli/internal/output"
)

// mutatorPrefixes triggers --dry-run behavior. Heuristic only — for tools with
// side effects that aren't covered by this list, the user can still inspect
// inputSchema via `searchlight tools describe <name>`.
var mutatorPrefixes = []string{
	"create_", "update_", "delete_", "send_", "add_", "remove_",
	"ingest_", "run_", "parse_", "cancel_", "pause_", "unpause_", "save_", "report_",
}

func isMutator(name string) bool {
	for _, p := range mutatorPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// registerDynamicTools loads the tools cache (silently, no fetch) and registers
// one cobra subcommand per cached tool. If the cache doesn't exist yet, this is
// a no-op — `auth login` and the startup freshness check (ensureFreshSchema)
// populate it automatically; `searchlight tools refresh` remains the manual
// escape hatch.
func registerDynamicTools(root *cobra.Command) {
	cs, err := globals.Schema.Load()
	if err != nil || cs == nil {
		return
	}
	existing := map[string]bool{}
	for _, sub := range root.Commands() {
		existing[sub.Name()] = true
	}
	for _, t := range cs.Tools {
		if existing[t.Name] {
			continue
		}
		root.AddCommand(buildToolCmd(t))
	}
}

type schemaProperty struct {
	Type        any    `json:"type"`
	Description string `json:"description"`
	Enum        []any  `json:"enum,omitempty"`
}

type parsedSchema struct {
	Type       string                    `json:"type"`
	Properties map[string]schemaProperty `json:"properties"`
	Required   []string                  `json:"required"`
}

func buildToolCmd(t mcp.ToolDefinition) *cobra.Command {
	parsed := parsedSchema{}
	_ = json.Unmarshal(t.InputSchema, &parsed)

	short := firstLine(t.Description)
	long := t.Description + "\n\n" + renderParamTable(parsed)

	var (
		jsonPayload string
		dryRun      bool
		flagValues  = map[string]*string{}
	)
	cmd := &cobra.Command{
		Use:           t.Name,
		Short:         short,
		Long:          long,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			payload, err := buildPayload(jsonPayload, flagValues, parsed.Properties)
			if err != nil {
				return err
			}
			// Required fields can be satisfied by either --json or per-property
			// flags, so we validate at runtime instead of via MarkFlagRequired.
			var missing []string
			for _, r := range parsed.Required {
				if _, ok := payload[r]; !ok {
					missing = append(missing, r)
				}
			}
			if len(missing) > 0 {
				return fmt.Errorf("missing required parameter(s): %s (pass via --json or --%s)", strings.Join(missing, ", "), missing[0])
			}
			if dryRun {
				return output.WriteValue(os.Stdout, map[string]any{
					"dry_run": true,
					"tool":    t.Name,
					"payload": payload,
				}, globals.Pretty)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			res, err := globals.MCP.CallTool(ctx, t.Name, payload)
			if err != nil {
				return err
			}
			return writeToolResult(res)
		},
	}

	cmd.Flags().StringVar(&jsonPayload, "json", "", "full JSON payload (preferred for agents)")
	if isMutator(t.Name) {
		cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print resolved payload without executing")
	}
	for propName := range parsed.Properties {
		// Per-property convenience flags are string-typed at the CLI surface
		// (cobra has no typed-from-schema flag). buildPayload coerces each value
		// to the type its JSON Schema declares before the call, because the
		// server validates types strictly and rejects a string where it wants a
		// number/array/object/bool.
		v := ""
		flagValues[propName] = &v
		desc := parsed.Properties[propName].Description
		cmd.Flags().StringVar(&v, propName, "", desc)
	}
	return cmd
}

func buildPayload(jsonPayload string, flags map[string]*string, props map[string]schemaProperty) (map[string]any, error) {
	out := map[string]any{}
	if jsonPayload != "" {
		if err := json.Unmarshal([]byte(jsonPayload), &out); err != nil {
			return nil, fmt.Errorf("--json is not valid JSON: %w", err)
		}
	}
	for name, ptr := range flags {
		if ptr == nil || *ptr == "" {
			continue
		}
		val, err := coerceFlagValue(name, *ptr, props[name])
		if err != nil {
			return nil, err
		}
		out[name] = val
	}
	return out, nil
}

// coerceFlagValue converts a named-flag string into the type its JSON Schema
// declares. Named flags arrive as strings, but the server validates types
// strictly, so a number/array/object/bool param must be converted here rather
// than passed through as a string (which the server would reject). When a value
// can't be coerced, the error names the flag and points at the --json escape
// hatch, which sidesteps shell-quoting entirely.
func coerceFlagValue(name, raw string, prop schemaProperty) (any, error) {
	types := schemaTypes(prop)
	// A string is always allowed if the schema permits it or declares no type;
	// pass it through unchanged to preserve the common case and avoid surprises.
	if len(types) == 0 || containsType(types, "string") {
		return raw, nil
	}
	switch {
	case containsType(types, "integer"):
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("--%s expects an integer, got %q", name, raw)
		}
		return n, nil
	case containsType(types, "number"):
		n, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("--%s expects a number, got %q", name, raw)
		}
		return n, nil
	case containsType(types, "boolean"):
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("--%s expects a boolean (true/false), got %q", name, raw)
		}
		return b, nil
	case containsType(types, "array"), containsType(types, "object"):
		var v any
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, fmt.Errorf("--%s expects %s; pass it as JSON (e.g. --%s '%s') or use --json '{...}'", name, types[0], name, exampleForType(types[0]))
		}
		return v, nil
	default:
		return raw, nil
	}
}

// schemaTypes normalizes a JSON Schema "type" (a string, or an array of strings
// for union types like ["string","null"]) into a slice of type names.
func schemaTypes(prop schemaProperty) []string {
	switch t := prop.Type.(type) {
	case string:
		return []string{t}
	case []any:
		var out []string
		for _, v := range t {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func containsType(types []string, want string) bool {
	for _, t := range types {
		if t == want {
			return true
		}
	}
	return false
}

func exampleForType(t string) string {
	if t == "object" {
		return `{"key":"value"}`
	}
	return `["a","b"]`
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, ".\n"); i > 0 {
		return s[:i]
	}
	return s
}

func renderParamTable(p parsedSchema) string {
	if len(p.Properties) == 0 {
		return "(no parameters)"
	}
	required := map[string]bool{}
	for _, r := range p.Required {
		required[r] = true
	}
	var b strings.Builder
	b.WriteString("Parameters:\n")
	for name, prop := range p.Properties {
		req := ""
		if required[name] {
			req = " (required)"
		}
		b.WriteString(fmt.Sprintf("  --%s%s\n", name, req))
		if prop.Description != "" {
			b.WriteString(fmt.Sprintf("      %s\n", prop.Description))
		}
	}
	return b.String()
}
