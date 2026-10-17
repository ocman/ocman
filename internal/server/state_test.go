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
	// Cache core and module migrations together. Migrating a core-only copy
	// would create a pre-migration backup for every server test database.
	path := testutil.TemplateCopy(t, "server-state.db", filepath.Join(t.TempDir(), "state.db"), func(path string) error {
		db, err := state.Open(statetest.At(t, path), ModuleSchemas()...)
		if err != nil {
			return err
		}
		return db.Close()
	})
	stateDB, err := state.Open(path, ModuleSchemas()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stateDB.Close() })
	return stateDB
}
