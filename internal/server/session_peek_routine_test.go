package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/state"
)

// Activity probes can insert a sidebar row before the next full list arrives.
// They must carry the same routine filter metadata as that list.
func TestSessionPeekPreservesRoutineFilter(t *testing.T) {
	for _, platform := range []string{"opencode", "r-owner:opencode"} {
		t.Run(platform, func(t *testing.T) {
			srv, _ := newPeekTestServer(t, platform, db.StatusBusy)
			ctx := t.Context()
			if err := srv.stateDB.CreateRoutine(ctx, state.Routine{ID: "rt", Name: "Nightly", Prompt: "check", Directory: "/src/foo", RemoteID: "local", SessionMode: "new", ScheduleKind: "none", ScheduleConfigJSON: "{}", CreatedAt: 1, UpdatedAt: 1}); err != nil {
				t.Fatal(err)
			}
			if _, _, err := srv.stateDB.ClaimRoutineRun(ctx, state.RoutineRun{ID: "run", RoutineID: "rt", Trigger: "manual", State: "running", OccurrenceAt: 1, CreatedAt: 1}); err != nil {
				t.Fatal(err)
			}
			if err := srv.stateDB.LinkRoutineRun(ctx, "run", platform, "s1", 2, false); err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			serveSessionRoute(srv.handleSession, response, httptest.NewRequest(http.MethodGet, "/api/session/s1?peek=1", nil))
			if response.Code != http.StatusOK {
				t.Fatalf("peek: %d %s", response.Code, response.Body.String())
			}
			var body struct{ Session db.Session }
			mustUnmarshal(t, response.Body.Bytes(), &body)
			if body.Session.RoutineID != "rt" {
				t.Fatalf("activity probe lost routine filter metadata: routineId = %q", body.Session.RoutineID)
			}
		})
	}
}
