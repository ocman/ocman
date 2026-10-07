package routines

import (
	"testing"

	"github.com/NoUseFreak/ocman/internal/sessionsvc"
	"github.com/NoUseFreak/ocman/internal/state"
)

func TestRoutineLinkedBeforeSessionPublished(t *testing.T) {
	var h *harness
	published := false
	h = newHarnessWithHooks(t, sessionsvc.Hooks{SessionCreated: func(info sessionsvc.CreatedSession) {
		published = true
		if info.RoutineID == "" {
			t.Error("provisional session has no routine filter metadata")
		}
		linked, err := h.db.RoutineSessions(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if linked[state.Key{Platform: info.Platform, SessionID: info.ID}] == "" {
			t.Error("routine session published before its filter metadata was linked")
		}
	}})
	routine, err := h.svc.Create(t.Context(), validInput())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.RunNow(t.Context(), routine.ID); err != nil {
		t.Fatal(err)
	}
	if !published {
		t.Fatal("session was never published")
	}
}
