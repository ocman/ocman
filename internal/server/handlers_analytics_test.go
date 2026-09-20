package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/state"
)

func TestAnalyticsOverviewRoute(t *testing.T) {
	srv := testServer(t)
	if err := srv.stateDB.CreateRoutine(t.Context(), state.Routine{
		ID: "routine", Name: "Routine", Directory: "/repo", RemoteID: "local",
		ScheduleKind: "manual", ScheduleConfigJSON: "{}", CreatedAt: 1, UpdatedAt: 1,
	}); err != nil {
		t.Fatal(err)
	}
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/analytics/overview", nil))
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

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/analytics/overview", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestDatabaseSizesRouteFiltersByDays(t *testing.T) {
	srv := testServer(t)
	now := time.Now()
	if err := srv.stateDB.RecordDatabaseSizeSamples(t.Context(), []state.DatabaseSizeSample{
		{Database: "opencode", SampledAt: now.Add(-48 * time.Hour).UnixMilli(), SizeBytes: 100},
		{Database: "opencode", SampledAt: now.UnixMilli(), SizeBytes: 200},
		{Database: "ocman", SampledAt: now.UnixMilli(), SizeBytes: 50},
	}); err != nil {
		t.Fatal(err)
	}
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/analytics/database-sizes?days=1", nil))
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

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/analytics/database-sizes", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestDatabaseSizesWithoutStateDB(t *testing.T) {
	rec := httptest.NewRecorder()
	(&Server{}).handleDatabaseSizes(rec, httptest.NewRequest(http.MethodGet, "/api/analytics/database-sizes", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "[]\n" {
		t.Fatalf("response = %d %q", rec.Code, rec.Body.String())
	}
}
