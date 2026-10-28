package tmux

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/testutil"
)

func TestLaunchFailureDoesNotExposeEnvironmentCredentials(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteScript(t, filepath.Join(dir, "tmux"), "#!/bin/sh\nprintf '%s\\n' \"$@\" >&2\nexit 1\n")
	t.Setenv("PATH", dir)
	err := Run(t.Context(), "new-session", "-e", "OPENCODE_SERVER_PASSWORD=test-server-credential", "-e", "OPENCODE_PASSWORD=test-client-credential", "-s", "fixture")
	if err == nil {
		t.Fatal("launch failure was not returned")
	}
	for _, secret := range []string{"test-server-credential", "test-client-credential"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatal("launch error exposed an environment credential")
		}
	}
}
