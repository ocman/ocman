package remote

import (
	"os"
	"testing"

	"github.com/ocman/ocman/internal/platforms/claude/provider/claudetest"
	"github.com/ocman/ocman/internal/platforms/opencode/apiv2"
)

// TestMain pins the installed OpenCode to v1 so tests never run the real
// opencode binary; v2 cases opt in with ocv2.SetInstalledV2(true).
func TestMain(m *testing.M) {
	ocv2.SetInstalledV2(false)
	os.Exit(claudetest.Run(m))
}
