package state

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	_ "modernc.org/sqlite"
)

func TestMigrateV47CreatesPermissionLifecycleSchema(t *testing.T) {
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	tx, err := raw.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateToV47(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	rows, err := raw.Query(`PRAGMA table_info(permission_lifecycle)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var columns []string
	var primaryKey []string
	for rows.Next() {
		var cid, notNull, pk int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &pk); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, name)
		if pk > 0 {
			primaryKey = append(primaryKey, name)
		}
	}
	wantColumns := []string{
		"platform", "session_id", "permission_id", "directory", "project_root", "requested_at",
		"judge_started_at", "judge_completed_at", "resolved_at", "evaluation_method",
		"evaluation_result", "resolution", "manually_preempted",
	}
	if !reflect.DeepEqual(columns, wantColumns) {
		t.Fatalf("columns = %v, want %v", columns, wantColumns)
	}
	if want := []string{"platform", "session_id", "permission_id"}; !reflect.DeepEqual(primaryKey, want) {
		t.Fatalf("primary key = %v, want %v", primaryKey, want)
	}
}

func TestPermissionApprovalStatsLifecycleUpdatesAndFilters(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	ctx := t.Context()
	day1 := int64(1_700_006_400_000) // 2023-11-15T00:00:00Z
	day2 := day1 + 86_400_000

	updates := []PermissionLifecycle{
		{Platform: "opencode", SessionID: "s1", PermissionID: "p1", Directory: "/repo", RequestedAt: day1 + 100},
		{Platform: "opencode", SessionID: "s1", PermissionID: "p1", JudgeStartedAt: day1 + 200, EvaluationMethod: "judge"},
		{Platform: "opencode", SessionID: "s1", PermissionID: "p1", JudgeCompletedAt: day1 + 500, ResolvedAt: day1 + 600, EvaluationResult: "safe", Resolution: "auto-approved"},
		// Empty and stale duplicate facts cannot erase or replace the completed lifecycle.
		{Platform: "opencode", SessionID: "s1", PermissionID: "p1", Directory: "/wrong", RequestedAt: day2, Resolution: "user-rejected"},
		{Platform: "opencode", SessionID: "s2", PermissionID: "p2", Directory: "/repo", RequestedAt: day1 + 1_000, EvaluationMethod: "judge"},
		{Platform: "opencode", SessionID: "s2", PermissionID: "p2", ResolvedAt: day1 + 1_700, Resolution: "user-once", ManuallyPreempted: true},
		{Platform: "opencode", SessionID: "s3", PermissionID: "p3", Directory: "/repo", RequestedAt: day2 + 100, EvaluationMethod: "cache", EvaluationResult: "cache-safe", ResolvedAt: day2 + 200, Resolution: "auto-approved"},
		{Platform: "opencode", SessionID: "s4", PermissionID: "p4", Directory: "/repo", RequestedAt: day2 + 300, EvaluationMethod: "denylist", EvaluationResult: "denylisted", ResolvedAt: day2 + 400, Resolution: "cancelled"},
		{Platform: "opencode", SessionID: "s5", PermissionID: "p5", Directory: "/repo", RequestedAt: day2 + 500, JudgeStartedAt: day2 + 600, JudgeCompletedAt: day2 + 1_100, ResolvedAt: day2 + 1_600, EvaluationMethod: "judge", EvaluationResult: "unsafe", Resolution: "user-rejected"},
		{Platform: "opencode", SessionID: "other", PermissionID: "p6", Directory: "/other", RequestedAt: day2, EvaluationMethod: "judge", EvaluationResult: "error"},
		{Platform: "opencode", SessionID: "worktree", PermissionID: "p7", Directory: "/src/.worktrees/repo/task", RequestedAt: day2 + 700, EvaluationMethod: "judge", EvaluationResult: "safe", Resolution: "auto-approved"},
	}
	for _, update := range updates {
		if err := db.UpsertPermissionLifecycle(ctx, update); err != nil {
			t.Fatalf("UpsertPermissionLifecycle(%+v): %v", update, err)
		}
	}

	stats, err := db.PermissionApprovalStats(ctx, day1, "/repo")
	if err != nil {
		t.Fatal(err)
	}
	if stats.EligibleRequests != 5 || stats.AutoApprovedCount != 2 || stats.JudgmentRequests != 3 || stats.ManualPreemptions != 1 {
		t.Fatalf("counts = %+v", stats)
	}
	if math.Abs(stats.AutoApprovedRate-0.4) > 1e-9 || math.Abs(stats.ManualPreemptionRate-1.0/3.0) > 1e-9 {
		t.Fatalf("rates = auto %v, preemption %v", stats.AutoApprovedRate, stats.ManualPreemptionRate)
	}
	if stats.MedianJudgmentDurationMs != 400 || stats.MedianManualResponseDurationMs != 700 {
		t.Fatalf("medians = judgment %d, manual %d", stats.MedianJudgmentDurationMs, stats.MedianManualResponseDurationMs)
	}
	wantDaily := []PermissionApprovalDaily{
		{Date: "2023-11-15", EvaluationResults: map[string]int{"safe": 1}, ManualPreemptions: 1},
		{Date: "2023-11-16", EvaluationResults: map[string]int{"cache-safe": 1, "denylisted": 1, "unsafe": 1}, ManualPreemptions: 0},
	}
	if !reflect.DeepEqual(stats.Daily, wantDaily) {
		t.Fatalf("daily = %#v, want %#v", stats.Daily, wantDaily)
	}

	day2Stats, err := db.PermissionApprovalStats(ctx, day2, "/repo")
	if err != nil {
		t.Fatal(err)
	}
	if day2Stats.EligibleRequests != 3 || len(day2Stats.Daily) != 1 || day2Stats.Daily[0].Date != "2023-11-16" {
		t.Fatalf("day-two filter = %+v", day2Stats)
	}

	allStats, err := db.PermissionApprovalStats(ctx, day1, "")
	if err != nil {
		t.Fatal(err)
	}
	if allStats.EligibleRequests != 7 {
		t.Fatalf("all-project eligible requests = %d, want 7", allStats.EligibleRequests)
	}

	projectStats, err := db.PermissionApprovalStats(ctx, day1, "/src/repo")
	if err != nil {
		t.Fatal(err)
	}
	if projectStats.EligibleRequests != 1 || projectStats.AutoApprovedCount != 1 {
		t.Fatalf("folded worktree project stats = %+v", projectStats)
	}
}

func TestUpsertPermissionLifecycleBackfillsSnapshotAfterResolution(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	ctx := t.Context()

	if err := db.UpsertPermissionLifecycle(ctx, PermissionLifecycle{
		Platform: "opencode", SessionID: "s1", PermissionID: "p1",
		ResolvedAt: 1_700_000_000_100, Resolution: PermissionResolutionUserOnce,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertPermissionLifecycle(ctx, PermissionLifecycle{
		Platform: "opencode", SessionID: "s1", PermissionID: "p1",
		Directory: "/repo", RequestedAt: 1_700_000_000_000,
	}); err != nil {
		t.Fatal(err)
	}

	stats, err := db.PermissionApprovalStats(ctx, 1, "/repo")
	if err != nil {
		t.Fatal(err)
	}
	if stats.EligibleRequests != 1 {
		t.Fatalf("eligible requests = %d, want 1", stats.EligibleRequests)
	}
}
