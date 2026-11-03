package server

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/opencodeconfig"
)

// Startup repair must see the MCP listener's finalized address and auth
// mode: it runs only after the listener settled, so it never races the
// listener rewriting mcpAddr (run with -race) and, when the listener falls
// back to the main port, never sends the dedicated listener's token to the
// address it failed to bind.
func TestStartupRepairUsesFinalizedMCPListener(t *testing.T) {
	for _, tc := range []struct {
		name      string
		bindFails bool
	}{
		{name: "listener up"},
		{name: "listener falls back", bindFails: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OCMAN_PLUGIN_DIR", t.TempDir())
			dir := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", dir)
			t.Setenv("OPENCODE_CONFIG", "")

			// The configured -mcp-addr: free for "listener up", held by
			// someone else for "listener falls back".
			blocker, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			addr := blocker.Addr().String()
			if tc.bindFails {
				defer blocker.Close()
			} else {
				_ = blocker.Close()
			}
			path := filepath.Join(dir, "opencode", "opencode.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			entryURL := "http://" + addr + "/mcp"
			if err := os.WriteFile(path, []byte(`{"mcp":{"ocman":{"type":"remote","url":"`+entryURL+`","enabled":true}}}`), 0o600); err != nil {
				t.Fatal(err)
			}

			srv := testServer(t)
			srv.WithMCPAddr(addr).WithMCPTokenFile(filepath.Join(t.TempDir(), "mcp-token"))
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)
			go func() { done <- srv.StartOnListener(ctx, ln) }()
			defer func() {
				cancel()
				select {
				case err := <-done:
					if err != nil {
						t.Error(err)
					}
				case <-time.After(10 * time.Second):
					t.Error("server did not stop")
				}
			}()

			// The v2 layout move proves the repair ran.
			var raw []byte
			for deadline := time.Now().Add(10 * time.Second); ; {
				raw, _ = os.ReadFile(path)
				if strings.Contains(string(raw), `"servers"`) {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("startup repair did not run: %s", raw)
				}
				time.Sleep(10 * time.Millisecond)
			}
			hasToken := strings.Contains(string(raw), opencodeconfig.AuthHeader(srv.mcpTokenFile))
			if hasToken == tc.bindFails {
				t.Fatalf("token reference present = %v, want %v: %s", hasToken, !tc.bindFails, raw)
			}
		})
	}
}
