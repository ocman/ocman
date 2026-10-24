package opencode

import (
	"context"
	"net/http"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// The RunShell request contract (method, path, body, agent default) and
// its 4xx/5xx handling are pinned by the mutatingOps tables in
// operations_more_test.go.

// TestRunShellOnPort_RejectsEmptyCommand guards against shipping an
// empty `!` to OpenCode (which would 400 with a less helpful error).
// Empty / whitespace-only commands are caller errors.
func TestRunShellOnPort_RejectsEmptyCommand(t *testing.T) {
	called := false
	port := fakePort(t, func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	})
	for _, cmd := range []string{"", "   ", "\t\n"} {
		err := runShellOnPort(context.Background(), port, platforms.RunShellRequest{
			SessionID: "ses_abc",
			Command:   cmd,
			Agent:     "build",
		})
		if err == nil {
			t.Errorf("runShellOnPort(%q): expected error, got nil", cmd)
		}
	}
	if called {
		t.Error("upstream was called for an empty command; should have short-circuited")
	}
}
