// Package statetest gives tests a fully migrated state database without
// paying for the migrations every time. Import it only from tests.
package statetest

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/testutil"
)

// Path returns the path of a fresh, fully migrated state.db in its own
// t.TempDir(). Pass it to state.Open with the same module schemas: the
// copy already has them migrated, so opening it neither migrates nor
// writes a pre-migration backup. Tests that exercise migrations
// themselves must keep opening an empty or legacy file instead.
func Path(tb testing.TB, schemas ...state.ModuleSchema) string {
	tb.Helper()
	return At(tb, filepath.Join(tb.TempDir(), "state.db"), schemas...)
}

// At writes a fresh, fully migrated state.db to path and returns path,
// for tests that need the database inside a directory they already use.
func At(tb testing.TB, path string, schemas ...state.ModuleSchema) string {
	tb.Helper()
	return testutil.TemplateCopy(tb, templateName(schemas), path, func(path string) error {
		db, err := state.Open(path, schemas...)
		if err != nil {
			return err
		}
		return db.Close()
	})
}

// templateName keys a template by its ordered module schemas, since
// state.Open migrates them in list order.
func templateName(schemas []state.ModuleSchema) string {
	var b strings.Builder
	b.WriteString("state")
	for _, schema := range schemas {
		fmt.Fprintf(&b, "+%s.%d", schema.Module, len(schema.Migrations))
	}
	b.WriteString(".db")
	return b.String()
}
