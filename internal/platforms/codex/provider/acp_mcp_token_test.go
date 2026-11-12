package codexprovider

import (
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/mcpauth"
)

// Codex sessions reach ocman's MCP listener with its bearer token; the
// header travels over the ACP pipe, never argv.
func TestACPServersSendBearerToken(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "mcp-token")
	if err := mcpauth.WritePrivateFile(tokenFile, []byte("tok-123")); err != nil {
		t.Fatal(err)
	}
	servers := newACPExecution(&backend{opts: Options{MCPAddr: "127.0.0.1:42", MCPTokenFile: tokenFile}}).servers("caller")
	if len(servers) != 1 || servers[0].Http == nil || servers[0].Http.Url != "http://127.0.0.1:42/mcp" {
		t.Fatalf("servers = %+v", servers)
	}
	headers := map[string]string{}
	for _, header := range servers[0].Http.Headers {
		headers[header.Name] = header.Value
	}
	if len(headers) != 3 || headers["Authorization"] != "Bearer tok-123" || headers[mcpauth.CallerPlatformHeader] != "codex" || headers[mcpauth.CallerSessionHeader] != "caller" {
		t.Fatalf("headers = %+v", headers)
	}
	// A missing token file sends no header; the listener then refuses.
	servers = newACPExecution(&backend{opts: Options{MCPAddr: "127.0.0.1:42", MCPTokenFile: filepath.Join(t.TempDir(), "missing")}}).servers("caller")
	if len(servers[0].Http.Headers) != 0 {
		t.Fatalf("headers without a token = %+v", servers[0].Http.Headers)
	}
	if len(newACPExecution(&backend{}).servers("caller")) != 0 {
		t.Fatal("MCP offered without a listener")
	}
}
