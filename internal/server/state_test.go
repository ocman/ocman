package server

import (
	"testing"

	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

func openTestStateDB(t *testing.T) *state.DB {
	t.Helper()
	stateDB, err := state.Open(statetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stateDB.Close() })
	return stateDB
}
