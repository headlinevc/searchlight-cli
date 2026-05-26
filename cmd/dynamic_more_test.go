package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/headlinevc/searchlight-cli/internal/mcp"
)

func TestRenderParamTable_Empty(t *testing.T) {
	got := renderParamTable(parsedSchema{})
	if got != "(no parameters)" {
		t.Errorf("empty params = %q, want '(no parameters)'", got)
	}
}

func TestRenderParamTable_MarksRequired(t *testing.T) {
	s := parsedSchema{
		Type: "object",
		Properties: map[string]schemaProperty{
			"company_domain": {Description: "Company domain"},
			"content":        {Description: "Body text"},
			"optional_field": {Description: "Optional"},
		},
		Required: []string{"company_domain", "content"},
	}
	got := renderParamTable(s)
	if !strings.Contains(got, "--company_domain (required)") {
		t.Errorf("missing (required) marker for company_domain: %q", got)
	}
	if !strings.Contains(got, "--content (required)") {
		t.Errorf("missing (required) marker for content: %q", got)
	}
	if !strings.Contains(got, "--optional_field\n") {
		t.Errorf("optional field should not have (required) marker: %q", got)
	}
	if !strings.Contains(got, "Company domain") {
		t.Errorf("missing description: %q", got)
	}
}

func TestBuildToolCmd_ReadToolHasNoDryRun(t *testing.T) {
	tool := mcp.ToolDefinition{
		Name:        "get_current_user",
		Description: "Identify the signed-in user.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}
	c := buildToolCmd(tool)
	if c.Flag("dry-run") != nil {
		t.Error("read tool should not have --dry-run flag")
	}
	if c.Flag("json") == nil {
		t.Error("every tool should expose --json")
	}
	if c.Use != "get_current_user" {
		t.Errorf("Use = %q, want get_current_user", c.Use)
	}
	if c.Short != "Identify the signed-in user" {
		t.Errorf("Short = %q, want first sentence", c.Short)
	}
}

func TestBuildToolCmd_MutatorHasDryRun(t *testing.T) {
	tool := mcp.ToolDefinition{
		Name:        "create_comment",
		Description: "Create a comment on a company.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"company_domain":{"type":"string","description":"Domain"},
				"content":{"type":"string","description":"Body"}
			},
			"required":["company_domain","content"]
		}`),
	}
	c := buildToolCmd(tool)
	if c.Flag("dry-run") == nil {
		t.Error("create_comment should have --dry-run flag")
	}
	if c.Flag("company_domain") == nil {
		t.Error("per-property flag for company_domain missing")
	}
	if c.Flag("content") == nil {
		t.Error("per-property flag for content missing")
	}
}

func TestBuildToolCmd_MissingRequiredFails(t *testing.T) {
	tool := mcp.ToolDefinition{
		Name:        "lookup_company",
		Description: "Look up a company.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{"domain":{"type":"string"}},
			"required":["domain"]
		}`),
	}
	c := buildToolCmd(tool)
	// Execute with no flags set — should fail at the missing-required check.
	c.SetContext(context.Background())
	c.SetArgs([]string{})
	err := c.RunE(c, []string{})
	if err == nil {
		t.Fatal("expected missing-required error")
	}
	if !strings.Contains(err.Error(), "domain") {
		t.Errorf("error %q should mention missing field 'domain'", err)
	}
}

func TestBuildToolCmd_DryRunReturnsPayloadNoCall(t *testing.T) {
	tool := mcp.ToolDefinition{
		Name:        "send_email",
		Description: "Send an email.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"to":{"type":"string"},"subject":{"type":"string"},"body":{"type":"string"}
			},
			"required":["to","subject","body"]
		}`),
	}
	c := buildToolCmd(tool)

	// Reach the dry-run flag and the per-property flags.
	_ = c.Flags().Set("dry-run", "true")
	_ = c.Flags().Set("to", "a@b.com")
	_ = c.Flags().Set("subject", "hi")
	_ = c.Flags().Set("body", "x")

	// MCP global must be nil-safe in dry-run path — set globals.MCP to nil to
	// catch any accidental real call. (The current implementation only reads
	// it when not in dry-run, so this asserts the contract.)
	globals.MCP = nil
	c.SetContext(context.Background())

	done := make(chan error, 1)
	go func() { done <- c.RunE(c, []string{}) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("dry-run errored: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("dry-run hung — likely tried to make a real call")
	}
}

// parsedSchema unmarshaling — guards against accidental schema-shape changes.
func TestParsedSchema_UnmarshalsToolDefinition(t *testing.T) {
	raw := json.RawMessage(`{
		"type":"object",
		"properties":{"foo":{"type":"string","description":"FOO"}},
		"required":["foo"]
	}`)
	var p parsedSchema
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if p.Type != "object" {
		t.Errorf("Type = %q", p.Type)
	}
	if _, ok := p.Properties["foo"]; !ok {
		t.Error("missing foo property")
	}
	if p.Properties["foo"].Description != "FOO" {
		t.Errorf("Description = %q", p.Properties["foo"].Description)
	}
	if len(p.Required) != 1 || p.Required[0] != "foo" {
		t.Errorf("Required = %v", p.Required)
	}
}
