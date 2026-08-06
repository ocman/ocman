package server

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// The dedicated MCP listener is the credential-free path for local MCP
// clients: it must serve them even when password auth is configured,
// because a native MCP client has no way to send an auth cookie.
func TestMCPListenerServesLocalClientWithPasswordAuth(t *testing.T) {
	srv := newWorkflowTestServer(t)
	srv.auth = newTestAuth(t, "hunter2")
	srv.mcpAddr = "127.0.0.1:0"
	defer srv.startMCPListener()()

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	req, err := http.NewRequest(http.MethodPost, "http://"+srv.mcpAddr+"/mcp", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post /mcp: %v", err)
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, payload)
	}
	if !strings.Contains(string(payload), `"tools"`) {
		t.Fatalf("want a tools/list result, got: %s", payload)
	}
}

// Refuse to serve MCP on a non-loopback address: the endpoint accepts
// the peer address as its credential, so it must not be reachable off
// the machine. Failing closed leaves /mcp on the main port under auth.
func TestMCPListenerRefusesNonLoopback(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:0", "192.0.2.1:8227", "not-an-addr"} {
		srv := newWorkflowTestServer(t)
		srv.mcpAddr = addr
		stop := srv.startMCPListener()
		stop()
		if srv.mcpAddr != addr {
			t.Errorf("%s: listener bound despite non-loopback address", addr)
		}
	}
}

func TestMCPListenerDisabledWhenAddrEmpty(t *testing.T) {
	srv := newWorkflowTestServer(t)
	srv.startMCPListener()()
	if srv.mcpAddr != "" {
		t.Fatalf("mcpAddr changed to %q", srv.mcpAddr)
	}
}

func TestMCPServerURL(t *testing.T) {
	tests := []struct {
		name    string
		addr    string
		mcpAddr string
		want    string
	}{
		{"dedicated listener wins", "127.0.0.1:8228", "127.0.0.1:8227", "http://127.0.0.1:8227/mcp"},
		{"falls back to main addr", "127.0.0.1:8228", "", "http://127.0.0.1:8228/mcp"},
		{"bare port gets a host", "", ":9000", "http://localhost:9000/mcp"},
		{"no addr at all", "", "", "http://localhost:8229/mcp"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{addr: tt.addr, mcpAddr: tt.mcpAddr}
			if got := s.mcpServerURL(); got != tt.want {
				t.Errorf("mcpServerURL() = %q, want %q", got, tt.want)
			}
		})
	}
}
