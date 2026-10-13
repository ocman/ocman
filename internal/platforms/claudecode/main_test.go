package claudecode

import (
	"os"
	"testing"
)

// The fake tmux launches this test binary as the project supervisor.
func TestMain(m *testing.M) {
	if RunProviderCommand(os.Args[1:]) {
		os.Exit(0)
	}
	os.Exit(m.Run())
}
