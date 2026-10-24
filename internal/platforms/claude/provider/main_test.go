package claudeprovider

import (
	"os"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms/claude/provider/claudetest"
)

func TestMain(m *testing.M) {
	os.Exit(claudetest.Run(m))
}
