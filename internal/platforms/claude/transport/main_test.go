package claudecode

import (
	"os"
	"testing"

	"github.com/ocman/ocman/internal/platforms/claude/provider/claudetest"
	"github.com/ocman/ocman/internal/testutil"
)

// The fake tmux launches this test binary as the project supervisor.
func TestMain(m *testing.M) {
	if RunProviderCommand(os.Args[1:]) {
		os.Exit(0)
	}
	isolated := testutil.IsolateHost()
	code := claudetest.Run(m)
	isolated()
	os.Exit(code)
}
