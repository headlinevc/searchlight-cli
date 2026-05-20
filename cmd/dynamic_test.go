package cmd

import (
	"reflect"
	"testing"
)

func TestIsMutator(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"create_comment", true},
		{"send_email", true},
		{"delete_attachment", true},
		{"update_task", true},
		{"add_list_companies", true},
		{"remove_list_people", true},
		{"ingest_knowledge", true},
		{"get_current_user", false},
		{"lookup_company", false},
		{"search_signa_entities", false},
		{"list_initiatives", false},
		{"read_parse_job", false},
	}
	for _, tc := range cases {
		if got := isMutator(tc.name); got != tc.want {
			t.Errorf("isMutator(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestBuildPayload_JSONOnly(t *testing.T) {
	out, err := buildPayload(`{"a":1,"b":"two"}`, nil)
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	want := map[string]any{"a": float64(1), "b": "two"}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("got %v, want %v", out, want)
	}
}

func TestBuildPayload_FlagsOverrideJSON(t *testing.T) {
	bVal := "override"
	cVal := "new"
	flags := map[string]*string{
		"b": &bVal,
		"c": &cVal,
	}
	out, err := buildPayload(`{"a":1,"b":"two"}`, flags)
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if out["a"] != float64(1) {
		t.Errorf("a = %v, want 1", out["a"])
	}
	if out["b"] != "override" {
		t.Errorf("b = %v, want override", out["b"])
	}
	if out["c"] != "new" {
		t.Errorf("c = %v, want new", out["c"])
	}
}

func TestBuildPayload_EmptyFlagsIgnored(t *testing.T) {
	empty := ""
	value := "set"
	flags := map[string]*string{
		"empty": &empty,
		"set":   &value,
	}
	out, err := buildPayload("", flags)
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if _, ok := out["empty"]; ok {
		t.Error("empty flag should not be in payload")
	}
	if out["set"] != "set" {
		t.Errorf("set = %v, want set", out["set"])
	}
}

func TestBuildPayload_InvalidJSON(t *testing.T) {
	_, err := buildPayload(`{not json`, nil)
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestFirstLine(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Hello world", "Hello world"},
		{"First sentence. Second sentence.", "First sentence"},
		{"Line one\nLine two", "Line one"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := firstLine(tc.in); got != tc.want {
			t.Errorf("firstLine(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
