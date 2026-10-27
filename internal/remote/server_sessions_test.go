package remote

import (
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
	"github.com/NoUseFreak/ocman/internal/state"
)

// A subagent of a routine run is tagged with the run's routine, so the hub's
// routine filter hides it alongside its parent.
func TestSessionsTagRoutineChildren(t *testing.T) {
	store := openInboxStore(t)
	if err := store.CreateRoutine(t.Context(), state.Routine{ID: "rt", Name: "Review", Prompt: "p", Directory: "/d", RemoteID: "local", SessionMode: "new", ScheduleKind: "none", ScheduleConfigJSON: "{}", CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ClaimRoutineRun(t.Context(), state.RoutineRun{ID: "run", RoutineID: "rt", Trigger: "manual", State: "running", OccurrenceAt: 1, CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := store.LinkRoutineRun(t.Context(), "run", "opencode", "parent", 2, false); err != nil {
		t.Fatal(err)
	}
	reg := platforms.NewRegistry()
	reg.Register(&fakePlatform{id: "opencode", sessions: []db.Session{{ID: "parent"}, {ID: "child", ParentID: "parent"}, {ID: "orphan", ParentID: "gone"}}})
	resp, err := NewServer(reg, localStubHost{}, "laptop", "test").UseInboxStore(store.DB).Sessions(t.Context(), &pb.SessionsReq{Platform: "opencode"})
	if err != nil {
		t.Fatal(err)
	}
	var rows []db.Session
	if err := unmarshalJSON(resp.Payload, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0].RoutineID != "rt" || rows[1].RoutineID != "rt" || rows[2].RoutineID != "" {
		t.Fatalf("rows=%+v", rows)
	}
}
