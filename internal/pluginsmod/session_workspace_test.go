package pluginsmod

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/state/statetest"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

func TestPrivateModelWorkspacesAreIsolatedAndRejectSymlinks(t *testing.T) {
	db := statetest.Open(t, Schema)
	d := localPageDescription()
	if err := db.DiscoverPlugin(t.Context(), d, "/plugin", strings.Repeat("a", 64), nil); err != nil {
		t.Fatal(err)
	}
	m := NewModule(Deps{Store: db})
	a, err := m.pluginModelWorkspace(t.Context(), d.ID, "workspace-a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.pluginModelWorkspace(t.Context(), d.ID, "workspace-b")
	if err != nil || a == b {
		t.Fatal("private workspace directories collide")
	}
	info, err := os.Stat(a)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatal("private workspace directory not private")
	}
	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), a); err != nil {
		t.Fatal(err)
	}
	if _, err := m.pluginModelWorkspace(t.Context(), d.ID, "workspace-a"); !errors.Is(err, plugins.ErrUnavailable) {
		t.Fatalf("symlinked model workspace admitted: %v", err)
	}
	if !filepath.IsAbs(b) {
		t.Fatal("model workspace not owner-resolved")
	}
}
