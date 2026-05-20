package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"testing"

	"github.com/headlinevc/searchlight-cli/internal/mcp"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = old })

	fn()
	w.Close()

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}

func TestWriteToolResult_JSONPassThrough(t *testing.T) {
	globals.Pretty = false
	res := &mcp.ToolCallResult{
		Content: []mcp.ToolContent{{Type: "text", Text: `{"company":"openai"}`}},
	}
	out := captureStdout(t, func() {
		if err := writeToolResult(res); err != nil {
			t.Errorf("writeToolResult: %v", err)
		}
	})
	if out != "{\"company\":\"openai\"}\n" {
		t.Errorf("got %q, want JSON pass-through", out)
	}
}

func TestWriteToolResult_NonJSONWraps(t *testing.T) {
	globals.Pretty = false
	res := &mcp.ToolCallResult{
		Content: []mcp.ToolContent{{Type: "text", Text: "plain text response"}},
	}
	out := captureStdout(t, func() {
		if err := writeToolResult(res); err != nil {
			t.Errorf("writeToolResult: %v", err)
		}
	})
	// Should wrap as {"text": "..."} JSON
	var got map[string]string
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not valid JSON: %q", out)
	}
	if got["text"] != "plain text response" {
		t.Errorf("text = %q, want plain text response", got["text"])
	}
}

func TestWriteToolResult_EmptyContent(t *testing.T) {
	globals.Pretty = false
	res := &mcp.ToolCallResult{Content: nil}
	out := captureStdout(t, func() {
		if err := writeToolResult(res); err != nil {
			t.Errorf("writeToolResult: %v", err)
		}
	})
	var got map[string]bool
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not valid JSON: %q", out)
	}
	if !got["empty"] {
		t.Errorf("empty = %v, want true", got["empty"])
	}
}

func TestWriteToolResult_IsErrorReturnsError(t *testing.T) {
	res := &mcp.ToolCallResult{
		Content: []mcp.ToolContent{{Type: "text", Text: `{"error":"boom"}`}},
		IsError: true,
	}
	_ = captureStdout(t, func() {
		err := writeToolResult(res)
		if err == nil {
			t.Error("expected error when IsError=true")
		}
	})
}
