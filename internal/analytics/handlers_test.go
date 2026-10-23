package analytics

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/state"
)

func TestSessionConcurrencyRoute(t *testing.T) {
	f := newFixture(t)
	rec := f.serve(http.MethodGet, "/api/analytics/session-concurrency?days=1&dir=/repo", "")
	var got db.SessionConcurrency
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil || len(got.Series) < 24 || len(got.Series) > 25 {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.serve(http.MethodPost, "/api/analytics/session-concurrency", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST = %d", rec.Code)
	}
	f.legacy.Close()
	f.legacy = nil
	_ = f.seedHistory.Store.Close()
	f.seedHistory.Store = nil
	if rec := f.serve(http.MethodGet, "/api/analytics/session-concurrency", ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("closed database = %d", rec.Code)
	}
	if rec := serve(routes(newTestModule(Deps{})), http.MethodGet, "/api/analytics/session-concurrency", ""); rec.Code == http.StatusOK {
		t.Fatal("expected missing database error")
	}
}

func TestAnalyticsOverviewRoute(t *testing.T) {
	f := newFixture(t)
	if err := f.deps.Store.CreateRoutine(t.Context(), state.Routine{
		ID: "routine", Name: "Routine", Directory: "/repo", RemoteID: "local",
		ScheduleKind: "manual", ScheduleConfigJSON: "{}", CreatedAt: 1, UpdatedAt: 1,
	}); err != nil {
		t.Fatal(err)
	}
	rec := f.serve(http.MethodGet, "/api/analytics/overview", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		InventoryScope  string         `json:"inventoryScope"`
		TotalSessions   int            `json:"totalSessions"`
		TotalProjects   int            `json:"totalProjects"`
		TotalRoutines   int            `json:"totalRoutines"`
		RoutineRuns     map[string]int `json:"routineRunsByStatus"`
		FactoryEpics    map[string]int `json:"factoryEpicsByStatus"`
		FactoryIssues   map[string]int `json:"factoryIssuesByStatus"`
		FactoryPhases   map[string]int `json:"factoryAttemptsByPhase"`
		FactoryOutcomes map[string]int `json:"factoryAttemptsByTerminalOutcome"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.InventoryScope != "local" || got.TotalSessions != 0 || got.TotalProjects != 0 || got.TotalRoutines != 1 {
		t.Fatalf("overview = %+v", got)
	}
	for name, counts := range map[string]map[string]int{
		"routine runs": got.RoutineRuns, "factory epics": got.FactoryEpics, "factory issues": got.FactoryIssues,
		"factory phases": got.FactoryPhases, "factory outcomes": got.FactoryOutcomes,
	} {
		if counts == nil || len(counts) != 0 {
			t.Fatalf("%s = %#v, want empty object", name, counts)
		}
	}

	rec = f.serve(http.MethodPost, "/api/analytics/overview", "")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestDatabaseSizesRouteFiltersByDays(t *testing.T) {
	f := newFixture(t)
	now := time.Now()
	if err := f.deps.Store.RecordDatabaseSizeSamples(t.Context(), []state.DatabaseSizeSample{
		{Database: "opencode", SampledAt: now.Add(-48 * time.Hour).UnixMilli(), SizeBytes: 100},
		{Database: "opencode", SampledAt: now.UnixMilli(), SizeBytes: 200},
		{Database: "ocman", SampledAt: now.UnixMilli(), SizeBytes: 50},
	}); err != nil {
		t.Fatal(err)
	}
	rec := f.serve(http.MethodGet, "/api/analytics/database-sizes?days=1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d: %s", rec.Code, rec.Body.String())
	}
	var got []state.DatabaseSizeSample
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].SizeBytes != 50 || got[1].SizeBytes != 200 {
		t.Fatalf("samples = %+v", got)
	}

	rec = f.serve(http.MethodPost, "/api/analytics/database-sizes", "")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestDatabaseSizesWithoutStateDB(t *testing.T) {
	rec := serve(routes(newTestModule(Deps{})), http.MethodGet, "/api/analytics/database-sizes", "")
	if rec.Code != http.StatusOK || rec.Body.String() != "[]\n" {
		t.Fatalf("response = %d %q", rec.Code, rec.Body.String())
	}
}
