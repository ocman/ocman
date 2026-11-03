package remote

import (
	"os"
	"testing"

	"github.com/ocman/ocman/internal/platforms/claude/provider/claudetest"
	"github.com/ocman/ocman/internal/testutil"
)

func TestMain(m *testing.M) {
	isolated := testutil.IsolateHost()
	code := claudetest.Run(m)
	isolated()
	os.Exit(code)
}
