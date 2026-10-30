package agentui

import (
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcptest"
	internalmcp "github.com/ocman/ocman/internal/mcp"
	"github.com/ocman/ocman/internal/testutil"
)

func TestRenderUITool(t *testing.T) {
	srv, err := mcptest.NewServer(t, internalmcp.ServerTools(internalmcp.Deps{Tools: (&Module{}).Tools()})...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	for _, tc := range []struct {
		name      string
		args      map[string]any
		wantError bool
	}{
		{"help", map[string]any{"action": "help"}, false},
		{"render", help()["example"].(map[string]any), false},
		{"invalid", map[string]any{"action": "render", "title": "x", "summary": "fallback", "messages": []any{}}, true},
		{"unknown", map[string]any{"action": "execute"}, true},
		{"missing action", map[string]any{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := testutil.CallTool(t, srv, "render_ui", tc.args)
			if res.IsError != tc.wantError {
				t.Fatalf("error=%v: %s", res.IsError, testutil.ResultText(res))
			}
			if tc.name == "render" {
				var out map[string]json.RawMessage
				if err := json.Unmarshal([]byte(testutil.ResultText(res)), &out); err != nil {
					t.Fatal(err)
				}
				if _, err := Validate(out["ocmanA2ui"]); err != nil {
					t.Fatalf("tool result is not renderable: %v", err)
				}
			}
		})
	}
}
