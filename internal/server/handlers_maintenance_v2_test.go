package server

import (
	"os"
	"testing"

	"github.com/NoUseFreak/ocman/internal/ocv2"
)

// TestMain pins the installed-OpenCode check so no test runs the real
// `opencode --version`.
func TestMain(m *testing.M) {
	ocv2.SetInstalledV2(false)
	os.Exit(m.Run())
}
