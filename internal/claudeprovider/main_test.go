package claudeprovider

import (
	"os"
	"testing"

	"github.com/NoUseFreak/ocman/internal/claudeprovider/claudetest"
)

func TestMain(m *testing.M) {
	os.Exit(claudetest.Run(m))
}
