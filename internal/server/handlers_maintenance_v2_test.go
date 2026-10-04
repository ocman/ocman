package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/ocmaint"
	"github.com/NoUseFreak/ocman/internal/ocv2"
)

// TestMain pins the installed-OpenCode check so no test runs the real
// `opencode --version`.
func TestMain(m *testing.M) {
	ocv2.SetInstalledV2(false)
	os.Exit(m.Run())
}

func TestMaintenanceActionV2IsConflict(t *testing.T) {
	defer ocv2.SetInstalledV2(true)()
	srv := (&Server{}).WithOpenCodeDBPath(filepath.Join(t.TempDir(), "opencode.db"))
	for name, action := range map[string]func(*ocmaint.Runner) error{
		"cleanup": (*ocmaint.Runner).Cleanup,
		"wrapped": func(*ocmaint.Runner) error { return fmt.Errorf("x: %w", ocmaint.ErrV2) },
	} {
		rec := httptest.NewRecorder()
		srv.maintenanceAction(action)(rec, httptest.NewRequest(http.MethodPost, "/", nil))
		if rec.Code != http.StatusConflict {
			t.Errorf("%s: %d %s, want 409", name, rec.Code, rec.Body.String())
		}
	}
}
