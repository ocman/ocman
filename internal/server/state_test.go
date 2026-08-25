package server

import (
	"database/sql"
	"testing"

	"github.com/NoUseFreak/ocman/internal/state"
)

func openTestStateDB(t *testing.T) *state.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	stateDB, err := state.OpenFromSQL(db)
	if err != nil {
		t.Fatal(err)
	}
	return stateDB
}
