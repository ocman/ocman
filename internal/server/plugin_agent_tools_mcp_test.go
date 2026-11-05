package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/module"
)

// Plugin agent tools reach the shared MCP handler through the plugins
// module's dynamic provider, while the built-in session dispatcher stays.
func TestMCPHandlerWiresPluginAgentTools(t *testing.T) {
	s := testServer(t)
	var dynamic bool
	for _, m := range s.modules() {
		if _, ok := m.(module.DynamicToolProvider); ok && m.Name() == "Plugins" {
			dynamic = true
		}
	}
	if !dynamic {
		t.Fatal("plugins module does not provide dynamic MCP tools")
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec := httptest.NewRecorder()
	s.mcpHandler().ServeHTTP(rec, req)
	var response struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &response) != nil {
		t.Fatalf("tools/list: %d %s", rec.Code, rec.Body)
	}
	found := false
	for _, tool := range response.Result.Tools {
		found = found || tool.Name == "plugin_session_tool"
	}
	if !found {
		t.Fatalf("plugin_session_tool missing: %s", rec.Body)
	}
}
