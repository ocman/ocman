package ocruntime

import (
	"os"
	"testing"

	"github.com/ocman/ocman/internal/testutil"
)

// TestMain isolates the host (see testutil.IsolateHost) so a runtime that
// is not stubbed cannot reach the user's tmux servers.
func TestMain(m *testing.M) {
	isolated := testutil.IsolateHost()
	code := m.Run()
	isolated()
	os.Exit(code)
}
