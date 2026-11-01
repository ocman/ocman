package claudeprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/mcpauth"
)

// Claude children get the listener's token through a private config file,
// so it never shows up in argv.
func TestMCPConfigArgCarriesTokenInPrivateFile(t *testing.T) {
	dir := t.TempDir()
	tokenFile := mcpauth.TokenPath(dir)
	if err := mcpauth.WritePrivateFile(tokenFile, []byte("tok-123")); err != nil {
		t.Fatal(err)
	}
	opts := Options{MCPTokenPath: tokenFile}
	config := MCPConfig("127.0.0.1:8227")
	arg := opts.mcpConfigArg(config)
	if strings.Contains(arg, "tok-123") || filepath.Dir(arg) != dir {
		t.Fatalf("argv value %q leaks the token or is not a file beside it", arg)
	}
	info, err := os.Stat(arg)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("config file = %v, %v; want 0600", info, err)
	}
	var doc struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	data, _ := os.ReadFile(arg)
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	server := doc.MCPServers["ocman"]
	if server.URL != "http://127.0.0.1:8227/mcp" || server.Type != "http" || server.Headers["Authorization"] != "Bearer tok-123" {
		t.Fatalf("config = %s", data)
	}
	if again := opts.mcpConfigArg(config); again != arg {
		t.Fatalf("same config wrote %q then %q", arg, again)
	}

	for name, tc := range map[string]struct {
		opts   Options
		config string
	}{
		"no listener":    {opts, ""},
		"no token file":  {Options{MCPTokenPath: filepath.Join(dir, "missing")}, config},
		"no token path":  {Options{}, config},
		"not JSON":       {opts, "nope"},
		"no ocman entry": {opts, `{"mcpServers":{}}`},
	} {
		if got := tc.opts.mcpConfigArg(tc.config); got != tc.config {
			t.Errorf("%s: mcpConfigArg = %q, want the inline config", name, got)
		}
	}
}

func TestHandleHookSendsBearerToken(t *testing.T) {
	previous := hookPID
	hookPID = func(context.Context) int { return 42 }
	t.Cleanup(func() { hookPID = previous })
	tokenFile := filepath.Join(t.TempDir(), "mcp-token")
	if err := mcpauth.WritePrivateFile(tokenFile, []byte("tok-123")); err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	HandleHook(strings.TrimPrefix(srv.URL, "http://"), tokenFile, "", "Stop", true, strings.NewReader(`{"session_id":"s1"}`), time.Second)
	select {
	case header := <-got:
		if header != "Bearer tok-123" {
			t.Fatalf("Authorization = %q", header)
		}
	default:
		t.Fatal("hook never posted")
	}
}

func TestHookCommandPassesTokenFile(t *testing.T) {
	cmd := HookCommand("/bin/ocman", "127.0.0.1:1", "/d/it's/mcp-token", "")
	if cmd != `"/bin/ocman" hook --addr 127.0.0.1:1 --token-file '/d/it'\''s/mcp-token'` {
		t.Fatalf("HookCommand = %s", cmd)
	}
	if cmd := HookCommand("/bin/ocman", "", "/d/mcp-token", "/j"); strings.Contains(cmd, "token") {
		t.Fatalf("token file without a listener: %s", cmd)
	}
	if DefaultOptions("/data").MCPTokenPath != mcpauth.TokenPath("/data") {
		t.Fatal("default options do not point at the data dir's token")
	}
}
