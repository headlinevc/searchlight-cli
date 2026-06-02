package cmd

import (
	"reflect"
	"strings"
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
	out, err := buildPayload(`{"a":1,"b":"two"}`, nil, nil)
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
	out, err := buildPayload(`{"a":1,"b":"two"}`, flags, nil)
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
	out, err := buildPayload("", flags, nil)
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
	_, err := buildPayload(`{not json`, nil, nil)
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

// Named flags arrive as strings; buildPayload must coerce each to the type its
// JSON Schema declares, because the server rejects a string where it wants a
// number/array/object/bool.
func TestBuildPayload_CoercesBySchemaType(t *testing.T) {
	num := "3"
	flo := "2.5"
	arr := `["https://a.com","https://b.com"]`
	obj := `{"q":"hi"}`
	boo := "true"
	str := "hello"
	flags := map[string]*string{
		"numResults": &num,
		"score":      &flo,
		"urls":       &arr,
		"summary":    &obj,
		"text":       &boo,
		"query":      &str,
	}
	props := map[string]schemaProperty{
		"numResults": {Type: "integer"},
		"score":      {Type: "number"},
		"urls":       {Type: "array"},
		"summary":    {Type: "object"},
		"text":       {Type: "boolean"},
		"query":      {Type: "string"},
	}
	out, err := buildPayload("", flags, props)
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if out["numResults"] != int64(3) {
		t.Errorf("numResults = %#v (%T), want int64(3)", out["numResults"], out["numResults"])
	}
	if out["score"] != float64(2.5) {
		t.Errorf("score = %#v, want 2.5", out["score"])
	}
	if got, ok := out["urls"].([]any); !ok || len(got) != 2 || got[0] != "https://a.com" {
		t.Errorf("urls = %#v, want 2-element string array", out["urls"])
	}
	if got, ok := out["summary"].(map[string]any); !ok || got["q"] != "hi" {
		t.Errorf("summary = %#v, want object {q:hi}", out["summary"])
	}
	if out["text"] != true {
		t.Errorf("text = %#v, want true", out["text"])
	}
	if out["query"] != "hello" {
		t.Errorf("query = %#v, want \"hello\"", out["query"])
	}
}

// A union type that includes "string" passes through unchanged (safest choice
// when the schema accepts a string).
func TestBuildPayload_UnionWithStringPassesThrough(t *testing.T) {
	v := "42"
	flags := map[string]*string{"flexible": &v}
	props := map[string]schemaProperty{
		"flexible": {Type: []any{"string", "number"}},
	}
	out, err := buildPayload("", flags, props)
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if out["flexible"] != "42" {
		t.Errorf("flexible = %#v, want string \"42\"", out["flexible"])
	}
}

func TestBuildPayload_CoercionErrorsPointAtJSON(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		typ     any
		wantSub string
	}{
		{"numResults", "abc", "integer", "expects an integer"},
		{"urls", "https://not-an-array.com", "array", "use --json"},
		{"flag", "notbool", "boolean", "expects a boolean"},
	}
	for _, tc := range cases {
		v := tc.value
		flags := map[string]*string{tc.name: &v}
		props := map[string]schemaProperty{tc.name: {Type: tc.typ}}
		_, err := buildPayload("", flags, props)
		if err == nil {
			t.Errorf("%s=%q: expected coercion error, got nil", tc.name, tc.value)
			continue
		}
		if !strings.Contains(err.Error(), tc.wantSub) {
			t.Errorf("%s=%q: error %q does not contain %q", tc.name, tc.value, err.Error(), tc.wantSub)
		}
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
