package server

import (
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestFactoryStopsSessionsWithoutDeletingHistory(t *testing.T) {
	for _, kind := range []string{"planning", "implementation"} {
		t.Run(kind, func(t *testing.T) {
			aborted := false
			platform := &fakePlatform{id: "opencode", sessions: []db.Session{{ID: "session"}}}
			platform.disposeFn = func(platforms.DisposeSessionRequest) error {
				t.Fatal("Factory deleted session history")
				return nil
			}
			platform.abortFn = func(req platforms.AbortRequest) error {
				aborted = req.SessionID == "session"
				return nil
			}
			registry := platforms.NewRegistry()
			registry.Register(platform)
			srv := New(nil, nil, "", registry, nil)
			session := factory.PlanningSession{Platform: "opencode", ID: "session"}
			var err error
			if kind == "planning" {
				err = (factoryPlanningLauncher{server: srv}).StopPlanningSession(t.Context(), session)
			} else {
				err = (factoryImplementationLauncher{server: srv}).StopImplementationSession(t.Context(), session)
			}
			if err != nil || !aborted || !platform.Owns(t.Context(), session.ID) {
				t.Fatalf("stop = %v, aborted = %v, history retained = %v", err, aborted, platform.Owns(t.Context(), session.ID))
			}
		})
	}
}
