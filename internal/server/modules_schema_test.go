package server

import (
	"testing"

	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

// The registered schemas must pass state.Open's ownership checks: unique
// modules, tables they create, and no foreign key into another module.
func TestModuleSchemasOpen(t *testing.T) {
	db, err := state.Open(statetest.Path(t), ModuleSchemas()...)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
}
