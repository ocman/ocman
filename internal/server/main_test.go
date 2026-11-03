package server

import (
	"os"
	"testing"

	"github.com/ocman/ocman/internal/forge/forgehttp"
	"github.com/ocman/ocman/internal/forge/forgejo"
	"github.com/ocman/ocman/internal/forge/github"
	"github.com/ocman/ocman/internal/platforms/claude/provider/claudetest"
	"github.com/ocman/ocman/internal/sourcecontrol"
	"github.com/ocman/ocman/internal/testutil"
)

// TestMain gives every Server the forge clients of a
// machine without credentials instead of running `gh auth token` and
// reading the developer's tea logins, and isolates the host (see
// testutil.IsolateHost) so the default native runtime cannot reach the
// user's tmux servers.
func TestMain(m *testing.M) {
	discoverForgeClients = hermeticForgeClients
	isolated := testutil.IsolateHost()
	code := claudetest.Run(m)
	isolated()
	os.Exit(code)
}

func hermeticForgeClients() *sourcecontrol.Clients {
	return &sourcecontrol.Clients{
		GitHub:  github.NewWithCredentials(github.DefaultAPIBase, "", forgehttp.InstrumentClient(nil)),
		Forgejo: forgejo.NewRegistryForTest(map[string]*forgejo.Client{}),
	}
}
