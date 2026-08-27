package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/state"
)

func TestPermissionStatsRoute(t *testing.T) {
	stateDB := openTestStateDB(t)
	now := time.Now()
	for _, lifecycle := range []state.PermissionLifecycle{
		{Platform: "opencode", SessionID: "project", PermissionID: "p1", Directory: "/src/repo", RequestedAt: now.Add(-time.Hour).UnixMilli(), JudgeStartedAt: now.Add(-59 * time.Minute).UnixMilli(), JudgeCompletedAt: now.Add(-58 * time.Minute).UnixMilli(), ResolvedAt: now.Add(-57 * time.Minute).UnixMilli(), EvaluationMethod: state.PermissionEvaluationJudge, EvaluationResult: state.PermissionEvaluationSafe, Resolution: state.PermissionResolutionAutoApproved},
		{Platform: "opencode", SessionID: "worktree", PermissionID: "p2", Directory: "/src/.worktrees/repo/task", RequestedAt: now.Add(-30 * time.Minute).UnixMilli(), EvaluationMethod: state.PermissionEvaluationCache, EvaluationResult: state.PermissionEvaluationCacheSafe, Resolution: state.PermissionResolutionAutoApproved},
		{Platform: "opencode", SessionID: "other", PermissionID: "p3", Directory: "/src/other", RequestedAt: now.Add(-15 * time.Minute).UnixMilli(), EvaluationMethod: state.PermissionEvaluationJudge, Resolution: state.PermissionResolutionUserOnce, ManuallyPreempted: true},
		{Platform: "opencode", SessionID: "old", PermissionID: "p4", Directory: "/src/old", RequestedAt: now.Add(-48 * time.Hour).UnixMilli(), EvaluationMethod: state.PermissionEvaluationDenylist, EvaluationResult: state.PermissionEvaluationDenylisted, Resolution: state.PermissionResolutionCancelled},
	} {
		if err := stateDB.UpsertPermissionLifecycle(t.Context(), lifecycle); err != nil {
			t.Fatalf("UpsertPermissionLifecycle: %v", err)
		}
	}

	srv := &Server{stateDB: stateDB} // No OpenCode read-only DB.
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}

	get := func(path string) (state.PermissionApprovalStats, map[string]json.RawMessage) {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: HTTP %d: %s", path, rec.Code, rec.Body.String())
		}
		var stats state.PermissionApprovalStats
		if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
			t.Fatalf("decode stats: %v", err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &fields); err != nil {
			t.Fatalf("decode fields: %v", err)
		}
		return stats, fields
	}

	all, fields := get("/api/permission-stats")
	if all.EligibleRequests != 4 || all.AutoApprovedCount != 2 {
		t.Fatalf("all-project stats = %+v", all)
	}
	for _, field := range []string{"eligibleRequests", "autoApprovedCount", "autoApprovedRate", "judgmentRequests", "manualPreemptions", "manualPreemptionRate", "medianJudgmentDurationMs", "medianManualResponseDurationMs", "daily"} {
		if _, ok := fields[field]; !ok {
			t.Errorf("response missing JSON field %q", field)
		}
	}

	query := url.Values{"dir": {" /src/repo/ "}}
	project, _ := get("/api/permission-stats?" + query.Encode())
	if project.EligibleRequests != 2 || project.AutoApprovedCount != 2 {
		t.Fatalf("project stats = %+v; managed worktree should be included", project)
	}

	recent, _ := get("/api/permission-stats?days=1")
	if recent.EligibleRequests != 3 {
		t.Fatalf("one-day stats eligible requests = %d, want 3", recent.EligibleRequests)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/permission-stats", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestPermissionStatsRouteReturnsInternalServerErrorOnStateQueryFailure(t *testing.T) {
	stateDB := openTestStateDB(t)
	srv := &Server{stateDB: stateDB}
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	if err := stateDB.Close(); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/permission-stats", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
}
