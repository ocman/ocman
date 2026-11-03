package local

import (
	"fmt"
	"os"
	"testing"
)

// TestMain points HOME at a temporary directory so machineRoot(), which
// every OpenCode ensure creates, never touches the developer's home.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "ocman-hostsvc-local-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = os.Setenv("HOME", home)
	_ = os.Unsetenv("OPENCODE_DB")
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}
