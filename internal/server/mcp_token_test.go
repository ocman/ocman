package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/mcpauth"
)

// The token is published 0600 for same-user clients and stays stable across
// restarts; OpenCode is pointed at the file, never the token itself.
func TestMCPListenerPublishesTokenFile(t *testing.T) {
	srv := testServer(t)
	srv.mcpAddr = "127.0.0.1:0"
	file := filepath.Join(t.TempDir(), "mcp-token")
	srv.WithMCPTokenFile(file)
	srv.startMCPListener()()
	info, err := os.Stat(file)
	if err != nil || info.Mode().Perm() != 0o600 || mcpauth.ReadToken(file) != srv.mcpToken || srv.mcpToken == "" {
		t.Fatalf("token file = %v, %v; want 0600 holding the listener token", info, err)
	}
	if srv.mcpClientTokenFile() != file {
		t.Fatalf("client token file = %q", srv.mcpClientTokenFile())
	}
	first := srv.mcpToken
	srv.mcpAddr = "127.0.0.1:0"
	srv.startMCPListener()()
	if srv.mcpToken != first {
		t.Fatal("token changed across listener restarts")
	}
	// Without the dedicated listener the main port's auth applies instead.
	srv.mcpAddr = ""
	if srv.mcpClientTokenFile() != "" {
		t.Fatal("main-port URL asked for the listener token")
	}
}

// The OpenCode installer and startup repair point the entry at the token
// file, so OpenCode authenticates without the token landing in its config.
func TestMCPConfigInstallReferencesTokenFile(t *testing.T) {
	srv, path := mcpConfigServer(t)
	srv.WithMCPTokenFile("/state/mcp-token")
	rec := httptest.NewRecorder()
	srv.handleMCPConfigStatus(rec, httptest.NewRequest(http.MethodGet, "/api/mcp/config", nil))
	if !strings.Contains(rec.Body.String(), `"wantAuthHeader":"Bearer {file:/state/mcp-token}"`) {
		t.Fatalf("status = %s", rec.Body.String())
	}
	rec = httptest.NewRecorder()
	srv.handleMCPConfigInstall(rec, httptest.NewRequest(http.MethodPost, "/api/mcp/config/install", nil))
	raw, err := os.ReadFile(path)
	if rec.Code != http.StatusOK || err != nil || !strings.Contains(string(raw), `"Authorization": "Bearer {file:/state/mcp-token}"`) {
		t.Fatalf("install = %d %s; config %s %v", rec.Code, rec.Body.String(), raw, err)
	}

	// An entry installed before the token gains the reference at startup.
	if err := os.WriteFile(path, []byte(`{"mcp":{"ocman":{"type":"remote","url":"http://127.0.0.1:8227/mcp","enabled":true}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ensureOpenCodeMCPLayout(srv.mcpServerURL(), srv.mcpClientTokenFile())
	if raw, _ := os.ReadFile(path); !strings.Contains(string(raw), "{file:/state/mcp-token}") {
		t.Fatalf("startup repair left %s", raw)
	}
}

// No token, no listener: it must never fall back to peer-only trust.
func TestMCPListenerDisabledWithoutToken(t *testing.T) {
	srv := testServer(t)
	srv.stateDB = nil
	srv.mcpAddr = "127.0.0.1:0"
	srv.startMCPListener()()
	if srv.mcpAddr != "" || !strings.Contains(srv.mcpListenErr, "token") {
		t.Fatalf("listener stayed up without a token: addr %q, err %q", srv.mcpAddr, srv.mcpListenErr)
	}
}

// The dedicated listener is reachable by every local UID, so the loopback
// peer alone must not be enough: /mcp and /hook need the install's token.
func TestMCPListenerRequiresBearerToken(t *testing.T) {
	srv := testServer(t)
	srv.mcpAddr = "127.0.0.1:0"
	defer srv.startMCPListener()()

	for _, tc := range []struct{ path, body string }{
		{"/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`},
		{"/hook", `{"event":"Stop","sessionID":"s1"}`},
	} {
		for name, header := range map[string]string{"missing": "", "wrong": "Bearer not-the-token", "basic": "Basic b2NtYW46eA=="} {
			req, err := http.NewRequest(http.MethodPost, "http://"+srv.mcpAddr+tc.path, strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			if header != "" {
				req.Header.Set("Authorization", header)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			payload, _ := io.ReadAll(io.LimitReader(resp.Body, 80))
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("%s %s token: status %d, want 401: %s", tc.path, name, resp.StatusCode, payload)
			}
		}
	}
}
