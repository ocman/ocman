package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mcpConfigServer returns a server whose MCP URL is fixed, with OpenCode's
// global config redirected into a temp dir.
func mcpConfigServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("OPENCODE_CONFIG", "")
	srv := &Server{mcpAddr: "127.0.0.1:8227"}
	return srv, filepath.Join(dir, "opencode", "opencode.json")
}

func TestHandleMCPConfigStatus(t *testing.T) {
	srv, path := mcpConfigServer(t)

	decode := func(t *testing.T) map[string]interface{} {
		t.Helper()
		rec := httptest.NewRecorder()
		srv.handleMCPConfigStatus(rec, httptest.NewRequest(http.MethodGet, "/api/mcp/config", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status: %d %s", rec.Code, rec.Body.String())
		}
		var got map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}

	// No config file yet: not configured, but installable.
	got := decode(t)
	if got["configured"] != false || got["editable"] != true {
		t.Fatalf("missing config: %v", got)
	}
	if got["wantUrl"] != "http://127.0.0.1:8227/mcp" {
		t.Fatalf("wantUrl = %v", got["wantUrl"])
	}

	// Configured with the right URL.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"mcp":{"ocman":{"type":"remote","url":"http://127.0.0.1:8227/mcp","enabled":true}}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := decode(t); got["configured"] != true {
		t.Fatalf("configured config reported as %v", got)
	}
}

func TestHandleMCPConfigInstall(t *testing.T) {
	srv, path := mcpConfigServer(t)

	rec := httptest.NewRecorder()
	srv.handleMCPConfigInstall(rec, httptest.NewRequest(http.MethodPost, "/api/mcp/config/install", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("install: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Installed  bool   `json:"installed"`
		Path       string `json:"path"`
		BackupPath string `json:"backupPath"`
		URL        string `json:"url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Installed || resp.Path != path || resp.URL != "http://127.0.0.1:8227/mcp" {
		t.Fatalf("install response: %+v", resp)
	}
	if resp.BackupPath != "" {
		t.Errorf("backup for a fresh config: %q", resp.BackupPath)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"ocman"`) {
		t.Fatalf("config lacks the ocman entry: %s", raw)
	}

	// A second install backs the previous file up.
	rec = httptest.NewRecorder()
	srv.handleMCPConfigInstall(rec, httptest.NewRequest(http.MethodPost, "/api/mcp/config/install", nil))
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.BackupPath == "" {
		t.Error("no backup taken when overwriting an existing config")
	} else if _, err := os.Stat(resp.BackupPath); err != nil {
		t.Errorf("backup %q not on disk: %v", resp.BackupPath, err)
	}
}

// A config ocman must not rewrite is the user's problem to fix: 409, and
// the file is left alone.
func TestHandleMCPConfigInstallRefusesJSONC(t *testing.T) {
	srv, path := mcpConfigServer(t)
	jsonc := filepath.Join(filepath.Dir(path), "opencode.jsonc")
	if err := os.MkdirAll(filepath.Dir(jsonc), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "{\n  // mine\n  \"theme\": \"x\"\n}"
	if err := os.WriteFile(jsonc, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.handleMCPConfigInstall(rec, httptest.NewRequest(http.MethodPost, "/api/mcp/config/install", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d: %s", rec.Code, rec.Body.String())
	}
	raw, err := os.ReadFile(jsonc)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != body {
		t.Errorf("jsonc config was modified: %s", raw)
	}
}

// The install route mutates a file in the user's home, so it must reject
// non-loopback and cross-origin callers.
func TestMCPConfigInstallRouteIsLocalhostOnly(t *testing.T) {
	srv := newWorkflowTestServer(t)
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		remoteAddr string
		origin     string
	}{
		{"remote peer", "192.0.2.1:1234", ""},
		{"hostile origin", "127.0.0.1:1234", "https://evil.example"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/mcp/config/install", nil)
			req.RemoteAddr = tt.remoteAddr
			req.Host = "localhost:8228"
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("want 403, got %d", rec.Code)
			}
		})
	}

	// GET is not an install.
	req := httptest.NewRequest(http.MethodGet, "/api/mcp/config/install", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Host = "localhost:8228"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET install: want 405, got %d", rec.Code)
	}
}
