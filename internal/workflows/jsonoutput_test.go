package workflows

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStripJSONFence(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"bare", `{"ok":true}`, `{"ok":true}`},
		{"bare with whitespace", "  {\"ok\":true}\n", `{"ok":true}`},
		{"json fence", "```json\n{\"ok\":true}\n```", `{"ok":true}`},
		{"upper json fence", "```JSON\n{\"ok\":true}\n```", `{"ok":true}`},
		{"plain fence", "```\n{\"ok\":true}\n```", `{"ok":true}`},
		{"fence with surrounding whitespace", "\n```json\n{\"ok\":true}\n```\n", `{"ok":true}`},
		{"fence with trailing text after close is dropped", "```json\n[1,2,3]\n```", `[1,2,3]`},
		{"no closing fence left alone", "```json\n{\"ok\":true}", "```json\n{\"ok\":true}"},
		{"text after closing fence left alone", "```json\n{\"ok\":true}\n```\nextra", "```json\n{\"ok\":true}\n```\nextra"},
		{"lone opening fence", "```", "```"},
		{"language tag with spaces not treated as fence", "```json here\n{}\n```", "```json here\n{}\n```"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripJSONFence(tt.in); got != tt.want {
				t.Fatalf("stripJSONFence(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}

	// A fenced JSON value validates after stripping; a bare fence does not.
	if !json.Valid([]byte(stripJSONFence("```json\n{\"ok\":true}\n```"))) {
		t.Fatal("stripped fenced JSON should be valid")
	}
}

func TestCompileOutputSchemaReferences(t *testing.T) {
	tests := []struct {
		name    string
		schema  any
		wantErr string
	}{
		{"boolean schema", true, ""},
		{"local reference", map[string]any{"$defs": map[string]any{"value": map[string]any{"type": "string"}}, "$ref": "#/$defs/value"}, ""},
		{"nested external dynamic reference", map[string]any{"allOf": []any{map[string]any{"$dynamicRef": "https://example.com/schema.json"}}}, "external references"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := compileOutputSchema(tt.schema)
			if (err == nil) != (tt.wantErr == "") || (err != nil && !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("compileOutputSchema() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}
