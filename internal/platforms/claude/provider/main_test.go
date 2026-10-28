package claudeprovider

import (
	"os"
	"testing"

	"github.com/ocman/ocman/internal/platforms/claude/provider/claudetest"
)

func TestMain(m *testing.M) {
	os.Exit(claudetest.Run(m))
}
