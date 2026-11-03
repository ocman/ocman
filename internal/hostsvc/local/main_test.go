package local

import (
	"os"
	"testing"

	"github.com/ocman/ocman/internal/testutil"
)

// TestMain isolates the host (see testutil.IsolateHost) so machineRoot(),
// which every OpenCode ensure creates, never touches the developer's home
// and no runtime reaches the user's tmux servers.
func TestMain(m *testing.M) {
	isolated := testutil.IsolateHost()
	code := m.Run()
	isolated()
	os.Exit(code)
}
