package opencode

import (
	"os"
	"testing"

	"github.com/NoUseFreak/ocman/internal/ocv2"
)

// TestMain pins the installed OpenCode to v1 so tests never run the real
// opencode binary; v2 cases opt in with ocv2.SetInstalledV2(true).
func TestMain(m *testing.M) {
	ocv2.SetInstalledV2(false)
	// Adapters built with New share td, so tests can seed and inspect the
	// discovery state an adapter uses (see discovery_helpers_test.go).
	newAdapterDiscovery = func() *Discovery { return td }
	os.Exit(m.Run())
}
