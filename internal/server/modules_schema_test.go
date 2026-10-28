package server

import (
	"testing"

	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
)

// The registered schemas must pass state.Open's ownership checks: unique
// modules, tables they create, and no foreign key into another module.
func TestModuleSchemasOpen(t *testing.T) {
	db, err := state.Open(statetest.Path(t), ModuleSchemas()...)
	if err != nil {
		t.Fatal(err)
	}
	catalogue, err := sessioncatalog.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := catalogue.List(t.Context(), "owner", 10, 0); err != nil || len(rows) != 0 {
		t.Fatalf("catalogue schema not registered: %+v %v", rows, err)
	}
	_ = db.Close()
}
