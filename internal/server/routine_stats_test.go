package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

func TestSummarizeRoutineRuns(t *testing.T) {
	runs := []state.RoutineRun{
		{State: "success", StartedAt: 100, FinishedAt: 300, Platform: "remote", SessionID: "s"},
		{State: "failure", StartedAt: 400, FinishedAt: 800, Platform: "remote", SessionID: "s"},
		{State: "running", StartedAt: 900, Platform: "remote", SessionID: "child"},
		{State: "interrupted", StartedAt: 1000, FinishedAt: 900, Platform: "offline", SessionID: "s"},
		{State: "failure"},
	}
	var calls int
	stats := summarizeRoutineRuns(t.Context(), runs, func(_ context.Context, key state.Key) (map[string]platforms.Usage, error) {
		calls++
		if key.Platform == "offline" {
			return nil, platforms.ErrNotFound
		}
		if key.SessionID == "child" {
			return map[string]platforms.Usage{"child": {Cost: 2, EstCost: 3}}, nil
		}
		return map[string]platforms.Usage{"s": {Cost: 1, EstCost: 2}, "child": {Cost: 2, EstCost: 3}}, nil
	})
	if stats.TotalRuns != 5 || stats.States["failure"] != 2 || stats.States["running"] != 1 || stats.AverageDuration == nil || *stats.AverageDuration != 300 {
		t.Fatalf("run stats: %+v", stats)
	}
	if calls != 3 || stats.CostSessions != 2 || stats.MissingSessions != 1 || stats.TotalCost != 3 || stats.TotalEstCost != 5 {
		t.Fatalf("cost stats: %+v, calls=%d", stats, calls)
	}
	empty := summarizeRoutineRuns(t.Context(), nil, nil)
	if empty.TotalRuns != 0 || empty.AverageDuration != nil || empty.CostSessions != 0 {
		t.Fatalf("empty: %+v", empty)
	}
}

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
		var stats routineStats
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
	var missing routineStats
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
