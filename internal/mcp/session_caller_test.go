package mcp

import (
	"net/http"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/ocman/ocman/internal/mcpauth"
)

func TestSessionCallerTransportContext(t *testing.T) {
	for _, tc := range []struct {
		name     string
		meta     map[string]any
		kind, id string
		want     *SessionCaller
		invalid  bool
	}{
		{name: "unbound"},
		{name: "unrelated metadata", meta: map[string]any{"other": "ignored"}},
		{name: "OpenCode", meta: map[string]any{"ai.opencode/sessionID": "native"}, want: &SessionCaller{"opencode", "native"}},
		{name: "Claude", kind: "claudecode", id: "uuid", want: &SessionCaller{"claudecode", "uuid"}},
		{name: "Codex", kind: "codex", id: "ocm_canonical", want: &SessionCaller{"codex", "ocm_canonical"}},
		{name: "matching contexts", meta: map[string]any{"ai.opencode/sessionID": "native"}, kind: "opencode", id: "native", want: &SessionCaller{"opencode", "native"}},
		{name: "conflicting contexts", meta: map[string]any{"ai.opencode/sessionID": "native"}, kind: "claudecode", id: "uuid", invalid: true},
		{name: "missing kind", id: "native", invalid: true},
		{name: "missing id", kind: "opencode", invalid: true},
		{name: "header whitespace", kind: "opencode", id: " native", invalid: true},
		{name: "empty metadata", meta: map[string]any{"ai.opencode/sessionID": ""}, invalid: true},
		{name: "wrong metadata type", meta: map[string]any{"ai.opencode/sessionID": 42}, invalid: true},
		{name: "null metadata", meta: map[string]any{"ai.opencode/sessionID": nil}, invalid: true},
		{name: "metadata whitespace", meta: map[string]any{"ai.opencode/sessionID": "native "}, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := mcplib.CallToolRequest{Header: http.Header{}}
			if tc.meta != nil {
				r.Params.Meta = &mcplib.Meta{AdditionalFields: tc.meta}
			}
			r.Header.Set(mcpauth.CallerPlatformHeader, tc.kind)
			r.Header.Set(mcpauth.CallerSessionHeader, tc.id)
			got, err := sessionCaller(r)
			if (err != nil) != tc.invalid || (got == nil) != (tc.want == nil) || got != nil && *got != *tc.want {
				t.Fatalf("caller = %+v, %v; want %+v, invalid=%v", got, err, tc.want, tc.invalid)
			}
		})
	}
}
