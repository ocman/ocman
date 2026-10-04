package ocmaint

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/ocv2"
)

// TestMain pins the installed-OpenCode check so no test runs the real
// `opencode --version`.
func TestMain(m *testing.M) {
	ocv2.SetInstalledV2(false)
	os.Exit(m.Run())
}

func TestCleanupAndRestoreRefuseOnV2(t *testing.T) {
	defer ocv2.SetInstalledV2(true)()
	h := &fakeHost{}
	r := newRunner(t, h, newOpenCodeDB(t), 1<<50)
	if err := r.Cleanup(); !errors.Is(err, ErrV2) {
		t.Errorf("Cleanup err = %v, want ErrV2", err)
	}
	if err := os.MkdirAll(filepath.Dir(r.DumpPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	// Restore checks for a dump first; give it one so the v2 guard is reached.
	if err := os.WriteFile(r.DumpPath(), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.Restore(); !errors.Is(err, ErrV2) {
		t.Errorf("Restore err = %v, want ErrV2", err)
	}
	if s := r.Status(); s.Running {
		t.Error("a refused job must not be running")
	}
	if len(h.stopped) != 0 {
		t.Errorf("refused job stopped instances: %v", h.stopped)
	}
}
