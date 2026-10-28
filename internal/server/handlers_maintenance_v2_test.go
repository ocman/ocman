package server

import (
	"os"
	"testing"

	"github.com/ocman/ocman/internal/forge/forgehttp"
	"github.com/ocman/ocman/internal/forge/forgejo"
	"github.com/ocman/ocman/internal/forge/github"
	"github.com/ocman/ocman/internal/platforms/claude/provider/claudetest"
	"github.com/ocman/ocman/internal/platforms/opencode/apiv2"
)

// TestMain pins the installed-OpenCode check so no test runs the real
// `opencode --version`, and gives every Server the forge clients of a
// machine without credentials instead of running `gh auth token` and
// reading the developer's tea logins.
func TestMain(m *testing.M) {
	ocv2.SetInstalledV2(false)
	discoverForgeClients = hermeticForgeClients
	os.Exit(claudetest.Run(m))
}

func hermeticForgeClients() *forgeClients {
	return &forgeClients{
		GitHub:  github.NewWithCredentials(github.DefaultAPIBase, "", forgehttp.InstrumentClient(nil)),
		Forgejo: forgejo.NewRegistryForTest(map[string]*forgejo.Client{}),
	}
}
