package output

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestWriteJSON_Compact(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteJSON(&buf, json.RawMessage(`{"a":1}`), false); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	got := buf.String()
	if got != "{\"a\":1}\n" {
		t.Errorf("got %q, want compact JSON with newline", got)
	}
}

func TestWriteJSON_Pretty(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteJSON(&buf, json.RawMessage(`{"a":1,"b":2}`), true); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "  \"a\":") {
		t.Errorf("pretty output missing 2-space indent: %q", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("pretty output missing trailing newline: %q", got)
	}
}

func TestWriteJSON_InvalidJSON_FallsBackToRaw(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteJSON(&buf, json.RawMessage(`not-json`), true); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	if got := buf.String(); got != "not-json\n" {
		t.Errorf("got %q, want raw fallback", got)
	}
}

func TestWriteValue_Compact(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteValue(&buf, map[string]int{"x": 1}, false); err != nil {
		t.Fatalf("WriteValue: %v", err)
	}
	if got := buf.String(); got != "{\"x\":1}\n" {
		t.Errorf("got %q", got)
	}
}

func TestWriteValue_Pretty(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteValue(&buf, map[string]int{"x": 1}, true); err != nil {
		t.Fatalf("WriteValue: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "  \"x\": 1") {
		t.Errorf("missing indent in %q", got)
	}
}

func TestHumanF_QuietSuppresses(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()

	old := os.Stderr
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = old })

	HumanF(true, "should-not-print %s", "test")
	w.Close()

	buf := make([]byte, 256)
	n, _ := r.Read(buf)
	if n != 0 {
		t.Errorf("stderr got %q, want empty when quiet=true", buf[:n])
	}
}

func TestHumanF_VerboseWrites(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()

	old := os.Stderr
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = old })

	HumanF(false, "hello %s", "world")
	w.Close()

	buf := make([]byte, 256)
	n, _ := r.Read(buf)
	got := string(buf[:n])
	if got != "hello world\n" {
		t.Errorf("got %q, want %q", got, "hello world\n")
	}
}
