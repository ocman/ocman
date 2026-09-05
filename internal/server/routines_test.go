package server

import (
	"context"
	"testing"

	"github.com/NoUseFreak/ocman/internal/routines"
)

func TestRunRoutinesStopsWithServerContext(t *testing.T) {
	s := &Server{stateDB: openTestStateDB(t)}
	s.routineSvc = routines.New(routines.Deps{Store: s.stateDB})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	s.runRoutines(ctx)
}

func TestRunRoutinesWithoutServiceReturns(t *testing.T) {
	(&Server{}).runRoutines(t.Context())
}
