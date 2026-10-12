package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/routines"
	"github.com/NoUseFreak/ocman/internal/state"
)

type routineUsagePlatform struct{ *fakePlatform }

func (*routineUsagePlatform) SessionUsage(context.Context, string) (map[string]platforms.Usage, error) {
	return map[string]platforms.Usage{"s": {Cost: 0.5, EstCost: 0.75}}, nil
}

func TestRoutineStatsHTTP(t *testing.T) {
	srv, handler, _ := routineHTTPServer(t)
	var routine state.Routine
	rec := doRoutineRequest(t, handler, http.MethodPost, "/api/routines", validRoutineBody)
	if err := json.Unmarshal(rec.Body.Bytes(), &routine); err != nil {
		t.Fatal(err)
	}
	// More than a history page: totals must cover every stored run.
	for i := range 60 {
		id := "run-" + strconv.Itoa(i)
		_, _, err := srv.stateDB.ClaimRoutineRun(t.Context(), state.RoutineRun{ID: id, RoutineID: routine.ID, State: "success", OccurrenceAt: int64(i + 1), CreatedAt: int64(i + 1), StartedAt: 100, FinishedAt: 300, Platform: "opencode", SessionID: "s"})
		if err != nil {
			t.Fatal(err)
		}
	}
	path := "/api/routines/" + routine.ID + "/stats"
	for _, adapter := range []platforms.Platform{&fakePlatform{id: "opencode"}, &routineUsagePlatform{&fakePlatform{id: "opencode"}}} {
		srv.registry.Register(adapter)
		rec = doRoutineRequest(t, handler, http.MethodGet, path, "")
		var stats routines.Stats
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &stats) != nil || stats.TotalRuns != 60 || stats.AverageDuration == nil || *stats.AverageDuration != 200 {
			t.Fatalf("stats: %d %s", rec.Code, rec.Body.String())
		}
		if _, supported := adapter.(platforms.UsageReader); supported {
			if stats.CostSessions != 1 || stats.TotalCost != 0.5 || stats.TotalEstCost != 0.75 {
				t.Fatalf("costs: %+v", stats)
			}
		} else if stats.MissingSessions != 1 {
			t.Fatalf("missing: %+v", stats)
		}
	}
	srv.registry.Unregister("opencode")
	rec = doRoutineRequest(t, handler, http.MethodGet, path, "")
	var missing routines.Stats
	if json.Unmarshal(rec.Body.Bytes(), &missing) != nil || missing.MissingSessions != 1 {
		t.Fatalf("disconnected: %s", rec.Body.String())
	}
	if rec = doRoutineRequest(t, handler, http.MethodGet, "/api/routines/missing/stats", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown: %d", rec.Code)
	}
	if rec = doRoutineRequest(t, handler, http.MethodPost, path, ""); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method: %d", rec.Code)
	}
}
