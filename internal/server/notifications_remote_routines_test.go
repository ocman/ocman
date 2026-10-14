package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/remote"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
	"github.com/NoUseFreak/ocman/internal/state"
)

func TestNotificationBadgeExcludesOwnerLocalRemoteRoutines(t *testing.T) {
	owner := testServer(t)
	p := &pushTestPlatform{fakePlatform: &fakePlatform{id: "opencode", sessions: []db.Session{
		{ID: "normal", Platform: "opencode", Status: db.StatusWaiting, TimeUpdated: time.Now().UnixMilli()},
		{ID: "routine", Platform: "opencode", Status: db.StatusWaiting, TimeUpdated: time.Now().UnixMilli()},
	}}}
	owner.registry.Register(p)
	if err := owner.stateDB.CreateRoutine(t.Context(), state.Routine{ID: "rt", Name: "Review", Prompt: "review", Directory: "/repo", RemoteID: "local", SessionMode: "new", ScheduleKind: "none", ScheduleConfigJSON: "{}", CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := owner.stateDB.ClaimRoutineRun(t.Context(), state.RoutineRun{ID: "run", RoutineID: "rt", Trigger: "manual", State: "running", OccurrenceAt: 1, CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := owner.stateDB.LinkRoutineRun(t.Context(), "run", "opencode", "routine", 2, false); err != nil {
		t.Fatal(err)
	}
	rpc := remote.NewServer(owner.registry, nil, "owner", "test").UseInboxStore(owner.stateDB).UseSessions(owner.SessionService())
	for _, prompt := range []bool{false, true} {
		p.sessions[1].PendingQuestion = prompt
		response, err := rpc.Sessions(t.Context(), &pb.SessionsReq{Platform: "opencode"})
		if err != nil {
			t.Fatal(err)
		}
		var sessions []db.Session
		if err := json.Unmarshal(response.Payload, &sessions); err != nil {
			t.Fatal(err)
		}
		if sessions[1].RoutineID != "rt" || p.sessions[1].RoutineID != "" {
			t.Fatalf("owner RPC must tag routines without mutating the platform cache: %+v", sessions)
		}
		for i := range sessions {
			sessions[i].Platform = "r-owner:opencode"
			sessions[i].RemoteID = "owner"
		}
		hub := testServer(t)
		hub.registry.Register(&pushTestPlatform{fakePlatform: &fakePlatform{id: "r-owner:opencode", sessions: sessions}, life: &platforms.SessionLifecycle{Status: db.StatusWaiting, LatestMessageRole: "assistant", LatestMessageID: "message"}})
		listed := append([]db.Session(nil), sessions...)
		if err := hub.applySessionState(t.Context(), listed); err != nil || listed[1].RoutineID != "rt" {
			t.Fatalf("hub erased the owner's routine association: %+v, %v", listed, err)
		}
		w := httptest.NewRecorder()
		hub.handleSessionsNotify(w, httptest.NewRequest(http.MethodGet, "/api/sessions/notify?includeDone=1", nil))
		var entries []notifyEntry
		if err := json.Unmarshal(w.Body.Bytes(), &entries); err != nil || len(entries) != 1 || entries[0].ID != "normal" {
			t.Fatalf("hub counted a remote routine: %s, %v", w.Body.String(), err)
		}
		rows, err := hub.pushSnapshot(t.Context())
		if err != nil || len(rows) != 1 || rows[0].Notification.BadgeCount != 1 {
			t.Fatalf("hub counted or alerted on remote routine: %+v, %v", rows, err)
		}
	}
}
