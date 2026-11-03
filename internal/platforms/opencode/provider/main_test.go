package opencode

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// Adapters built with New share td, so tests can seed and inspect the
	// discovery state an adapter uses (see discovery_helpers_test.go).
	newAdapterDiscovery = func() *Discovery { return td }
	os.Exit(m.Run())
}
