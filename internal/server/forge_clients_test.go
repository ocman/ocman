package server

import (
	"os"
	"path/filepath"
	"testing"
)

// machineForgeCredentials gives the process a tea login and a GitHub token,
// as on a developer's machine.
func machineForgeCredentials(t *testing.T) {
	t.Helper()
	config := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("GITHUB_TOKEN", "machine-token")
	if err := os.MkdirAll(filepath.Join(config, "tea"), 0o700); err != nil {
		t.Fatal(err)
	}
	login := "logins:\n  - name: machine\n    url: https://code.example.com\n    token: MACHINE_TOKEN\n"
	if err := os.WriteFile(filepath.Join(config, "tea", "config.yml"), []byte(login), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestNewForgeClientsReadsMachineCredentials(t *testing.T) {
	machineForgeCredentials(t)
	clients := newForgeClients()
	if !clients.Forgejo.Knows("code.example.com") || clients.GitHub.Token() != "machine-token" {
		t.Fatalf("production discovery ignored machine credentials: forgejo hosts %v, github authenticated %v", clients.Forgejo.Hosts(), clients.GitHub.Authenticated())
	}
}

func TestTestServersIgnoreMachineForgeCredentials(t *testing.T) {
	machineForgeCredentials(t)
	srv := New(nil, nil, "", nil, nil)
	if srv.knowsForgejo("code.example.com") || srv.integrations.GitHub.Authenticated() {
		t.Fatal("a test server read the machine's forge credentials")
	}
}
