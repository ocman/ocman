package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms/claude/provider/claudetest"
	"github.com/NoUseFreak/ocman/internal/platforms/claude/transport"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode/apiv2"
)

// TestMain pins the installed OpenCode to v1 so tests never run the real
// opencode binary; v2 cases opt in with ocv2.SetInstalledV2(true).
// It also serves `ocman provider <id>` when a test's provider runtime
// launches the test binary as the supervisor executable.
func TestMain(m *testing.M) {
	if claudecode.RunProviderCommand(os.Args[1:]) {
		os.Exit(0)
	}
	skillCatalog = map[string][]byte{}
	for _, name := range []string{"ocman-factory", "ocman-routines", "ocman-sessions", "ocman-inbox", "ocman-artifacts"} {
		data, err := os.ReadFile(filepath.Join("..", "..", ".opencode", "skills", name, "SKILL.md"))
		if err != nil {
			panic(err)
		}
		skillCatalog[name] = data
	}
	if os.Getenv("OCMAN_TEST_NO_OPENCODE") == "" {
		ocv2.SetInstalledV2(false)
	}
	os.Exit(claudetest.Run(m))
}
