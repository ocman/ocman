package server

import (
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
	"github.com/NoUseFreak/ocman/internal/testutil"
)

func openTestStateDB(t testing.TB) *state.DB {
	t.Helper()
	stateDB, err := state.Open(testStatePath(t), ModuleSchemas()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stateDB.Close() })
	return stateDB
}

// testStatePath returns a fresh state.db in its own t.TempDir() with the core
// and module migrations already applied.
func testStatePath(t testing.TB) string {
	t.Helper()
	return testStateAt(t, filepath.Join(t.TempDir(), "state.db"))
}

// testStateAt writes a fresh state.db with core and module migrations applied
// to path. Migrating a core-only copy would run the module migrations and
// write a pre-migration backup for every server test database.
func testStateAt(t testing.TB, path string) string {
	t.Helper()
	return testutil.TemplateCopy(t, "server-state.db", path, func(path string) error {
		db, err := state.Open(statetest.At(t, path), ModuleSchemas()...)
		if err != nil {
			return err
		}
		return db.Close()
	})
}
