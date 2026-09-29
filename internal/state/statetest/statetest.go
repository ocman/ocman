// Package statetest gives tests a fully migrated state database without
// paying for the migrations every time. Import it only from tests.
package statetest

import (
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/testutil"
)

// Path returns the path of a fresh, fully migrated state.db in its own
// t.TempDir(). Pass it to state.Open. Tests that exercise migrations
// themselves must keep opening an empty or legacy file instead.
func Path(tb testing.TB) string {
	tb.Helper()
	return At(tb, filepath.Join(tb.TempDir(), "state.db"))
}

// At writes a fresh, fully migrated state.db to path and returns path,
// for tests that need the database inside a directory they already use.
func At(tb testing.TB, path string) string {
	tb.Helper()
	return testutil.TemplateCopy(tb, "state.db", path, build)
}

func build(path string) error {
	db, err := state.Open(path)
	if err != nil {
		return err
	}
	return db.Close()
}
