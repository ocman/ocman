package state

import (
	"database/sql"
	"testing"
)

func TestPromptSchedulePersistenceAndAtomicClaim(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	schedule := PromptSchedule{ID: "ps_1", Directory: "/repo", RemoteID: "local", Prompt: "exact\n", RunAt: 1000, State: "scheduled", TimingType: "once", Timezone: "UTC", Enabled: true, SessionMode: "fresh", CreatedAt: 500, UpdatedAt: 500}
	if err := db.CreatePromptSchedule(t.Context(), schedule); err != nil {
		t.Fatal(err)
	}

	if _, ok, err := db.ClaimPromptSchedule(t.Context(), schedule.ID, 1000, false); err != nil || !ok {
		t.Fatalf("first claim: ok=%v err=%v", ok, err)
	}
	if _, ok, err := db.ClaimPromptSchedule(t.Context(), schedule.ID, 1000, false); err != nil || ok {
		t.Fatalf("duplicate claim: ok=%v err=%v", ok, err)
	}
	if err := db.LinkPromptScheduleSession(t.Context(), schedule.ID, "opencode", "ses_1", 1100); err != nil {
		t.Fatal(err)
	}
	if err := db.FinishPromptSchedule(t.Context(), schedule.ID, "completed", "", 1200); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetPromptSchedule(t.Context(), schedule.ID)
	if err != nil || got.Prompt != "exact\n" || got.SessionID != "ses_1" || got.State != "completed" {
		t.Fatalf("schedule=%+v err=%v", got, err)
	}
}

func TestPromptScheduleClaimsDueRowsOneAtATimeAndCancel(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	for _, schedule := range []PromptSchedule{
		{ID: "due", Directory: "/a", RemoteID: "local", Prompt: "a", RunAt: 1000, State: "scheduled", TimingType: "once", Timezone: "UTC", Enabled: true, SessionMode: "fresh", CreatedAt: 1, UpdatedAt: 1},
		{ID: "later", Directory: "/a", RemoteID: "local", Prompt: "b", RunAt: 3000, State: "scheduled", TimingType: "once", Timezone: "UTC", Enabled: true, SessionMode: "fresh", CreatedAt: 2, UpdatedAt: 2},
		{ID: "other", Directory: "/b", RemoteID: "local", Prompt: "c", RunAt: 1000, State: "scheduled", TimingType: "once", Timezone: "UTC", Enabled: true, SessionMode: "fresh", CreatedAt: 3, UpdatedAt: 3},
	} {
		if err := db.CreatePromptSchedule(t.Context(), schedule); err != nil {
			t.Fatal(err)
		}
	}
	first, ok, err := db.ClaimNextDuePromptSchedule(t.Context(), 2000)
	if err != nil || !ok || first.ID != "due" {
		t.Fatalf("first=%+v ok=%v err=%v", first, ok, err)
	}
	second, ok, err := db.ClaimNextDuePromptSchedule(t.Context(), 2000)
	if err != nil || !ok || second.ID != "other" {
		t.Fatalf("second=%+v ok=%v err=%v", second, ok, err)
	}
	if _, ok, err := db.ClaimNextDuePromptSchedule(t.Context(), 2000); err != nil || ok {
		t.Fatalf("third claim: ok=%v err=%v", ok, err)
	}
	listed, err := db.ListPromptSchedules(t.Context(), "/a", "local")
	if err != nil || len(listed) != 2 {
		t.Fatalf("listed=%+v err=%v", listed, err)
	}
	canceled, ok, err := db.CancelPromptSchedule(t.Context(), "later", 2100)
	if err != nil || !ok || canceled.State != "canceled" {
		t.Fatalf("canceled=%+v ok=%v err=%v", canceled, ok, err)
	}
}

func TestPromptScheduleRecoveryFailsRunningOnly(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	for _, schedule := range []PromptSchedule{
		{ID: "running", Directory: "/a", RemoteID: "local", Prompt: "a", RunAt: 1, State: "scheduled", TimingType: "once", Timezone: "UTC", Enabled: true, SessionMode: "fresh", CreatedAt: 1, UpdatedAt: 1},
		{ID: "waiting", Directory: "/a", RemoteID: "local", Prompt: "b", RunAt: 2, State: "scheduled", TimingType: "once", Timezone: "UTC", Enabled: true, SessionMode: "fresh", CreatedAt: 1, UpdatedAt: 1},
	} {
		if err := db.CreatePromptSchedule(t.Context(), schedule); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok, err := db.ClaimPromptSchedule(t.Context(), "running", 3, true); err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	runningRows, err := db.ListRunningPromptSchedules(t.Context())
	if err != nil || len(runningRows) != 1 || runningRows[0].ID != "running" {
		t.Fatalf("running rows=%+v err=%v", runningRows, err)
	}
	if err := db.FailRunningPromptSchedules(t.Context(), 4, "interrupted"); err != nil {
		t.Fatal(err)
	}
	running, _ := db.GetPromptSchedule(t.Context(), "running")
	waiting, _ := db.GetPromptSchedule(t.Context(), "waiting")
	if running.State != "failed" || running.Error != "interrupted" || waiting.State != "scheduled" {
		t.Fatalf("running=%+v waiting=%+v", running, waiting)
	}
}

func TestRecurringPromptSchedulePersistenceAndDisabledClaim(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	schedule := PromptSchedule{
		ID: "recurring", Directory: "/repo", RemoteID: "local", Prompt: "go", RunAt: 1000,
		State: "scheduled", TimingType: "interval", IntervalMinutes: 15, Timezone: "Europe/Brussels",
		Enabled: false, SessionMode: "reuse", CreatedAt: 1, UpdatedAt: 1,
	}
	if err := db.CreatePromptSchedule(t.Context(), schedule); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := db.ClaimNextDuePromptSchedule(t.Context(), 2000); err != nil || ok {
		t.Fatalf("disabled claim: ok=%v err=%v", ok, err)
	}
	got, err := db.SetPromptScheduleEnabled(t.Context(), schedule.ID, true, 1000, 2)
	if err != nil || !got.Enabled || got.Timezone != schedule.Timezone || got.SessionMode != "reuse" {
		t.Fatalf("enabled=%+v err=%v", got, err)
	}
	if _, ok, err := db.ClaimNextDuePromptSchedule(t.Context(), 2000); err != nil || !ok {
		t.Fatalf("enabled claim: ok=%v err=%v", ok, err)
	}
	if err := db.ReschedulePromptSchedule(t.Context(), schedule.ID, 3000, "temporary failure", 2100); err != nil {
		t.Fatal(err)
	}
	got, _ = db.GetPromptSchedule(t.Context(), schedule.ID)
	if got.State != "scheduled" || got.RunAt != 3000 || got.Error != "temporary failure" {
		t.Fatalf("rescheduled recurring schedule=%+v", got)
	}
	if _, ok, err := db.ClaimNextDuePromptSchedule(t.Context(), 3000); err != nil || !ok {
		t.Fatalf("reclaim: ok=%v err=%v", ok, err)
	}
	if err := db.CompletePromptSchedule(t.Context(), schedule.ID, 4000, 3100); err != nil {
		t.Fatal(err)
	}
}

func TestPromptScheduleClaimSkipsArchivedProject(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	for _, schedule := range []PromptSchedule{
		{ID: "archived", Directory: "/src/.worktrees/repo/topic", RemoteID: "local", Prompt: "a", RunAt: 1000, State: "scheduled", TimingType: "once", Timezone: "UTC", Enabled: true, SessionMode: "fresh", CreatedAt: 1, UpdatedAt: 1},
		{ID: "unrelated", Directory: "/other", RemoteID: "local", Prompt: "b", RunAt: 1000, State: "scheduled", TimingType: "once", Timezone: "UTC", Enabled: true, SessionMode: "fresh", CreatedAt: 1, UpdatedAt: 1},
	} {
		if err := db.CreatePromptSchedule(t.Context(), schedule); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.ArchiveProject(t.Context(), "local", "/src/repo"); err != nil {
		t.Fatal(err)
	}
	got, ok, err := db.ClaimNextDuePromptSchedule(t.Context(), 2000)
	if err != nil || !ok || got.ID != "unrelated" {
		t.Fatalf("claim=%+v ok=%v err=%v", got, ok, err)
	}
	if err := db.UnarchiveProject(t.Context(), "local", "/src/repo"); err != nil {
		t.Fatal(err)
	}
	got, ok, err = db.ClaimNextDuePromptSchedule(t.Context(), 2000)
	if err != nil || !ok || got.ID != "archived" {
		t.Fatalf("claim after unarchive=%+v ok=%v err=%v", got, ok, err)
	}
}

// The archived-project check is per host: archiving /repo on the hub must
// not stop a schedule that runs the same path on another machine.
func TestPromptScheduleClaimArchiveCheckIsPerHost(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	if err := db.CreatePromptSchedule(t.Context(), PromptSchedule{
		ID: "remote", Directory: "/repo", RemoteID: "r-A", Prompt: "a", RunAt: 1000,
		State: "scheduled", TimingType: "once", Timezone: "UTC", Enabled: true,
		SessionMode: "fresh", CreatedAt: 1, UpdatedAt: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.ArchiveProject(t.Context(), "local", "/repo"); err != nil {
		t.Fatal(err)
	}
	got, ok, err := db.ClaimNextDuePromptSchedule(t.Context(), 2000)
	if err != nil || !ok || got.ID != "remote" {
		t.Fatalf("claim=%+v ok=%v err=%v; want the remote schedule to run despite the local archive", got, ok, err)
	}

	// Archiving the remote's own copy does stop it.
	if err := db.ReschedulePromptSchedule(t.Context(), got.ID, 1000, "", 2100); err != nil {
		t.Fatal(err)
	}
	if err := db.ArchiveProject(t.Context(), "r-A", "/repo"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := db.ClaimNextDuePromptSchedule(t.Context(), 3000); err != nil || ok {
		t.Fatalf("claim after the remote's own archive: ok=%v err=%v, want none", ok, err)
	}
}

func TestPromptScheduleClaimSkipsArchivedReuseSession(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	for _, schedule := range []PromptSchedule{
		{ID: "archived", Directory: "/repo", RemoteID: "local", Prompt: "a", Platform: "opencode", SessionID: "s1", RunAt: 1000, State: "scheduled", TimingType: "once", Timezone: "UTC", Enabled: true, SessionMode: "reuse", CreatedAt: 1, UpdatedAt: 1},
		{ID: "unrelated", Directory: "/repo", RemoteID: "local", Prompt: "b", Platform: "opencode", SessionID: "s2", RunAt: 1000, State: "scheduled", TimingType: "once", Timezone: "UTC", Enabled: true, SessionMode: "reuse", CreatedAt: 1, UpdatedAt: 1},
	} {
		if err := db.CreatePromptSchedule(t.Context(), schedule); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.db.Exec(`UPDATE prompt_schedule SET platform = 'opencode', session_id = CASE id WHEN 'archived' THEN 's1' ELSE 's2' END`); err != nil {
		t.Fatal(err)
	}
	if err := db.ArchiveSession(t.Context(), "opencode", "s1", 1); err != nil {
		t.Fatal(err)
	}
	if err := db.ArchiveProject(t.Context(), "local", "/other"); err != nil {
		t.Fatal(err)
	}
	got, ok, err := db.ClaimNextDuePromptSchedule(t.Context(), 2000)
	if err != nil || !ok || got.ID != "unrelated" {
		t.Fatalf("claim=%+v ok=%v err=%v", got, ok, err)
	}
	if err := db.UnarchiveSession(t.Context(), "opencode", "s1"); err != nil {
		t.Fatal(err)
	}
	got, ok, err = db.ClaimNextDuePromptSchedule(t.Context(), 2000)
	if err != nil || !ok || got.ID != "archived" {
		t.Fatalf("claim after unarchive=%+v ok=%v err=%v", got, ok, err)
	}
}

func TestCancelRunningPromptSchedulePreventsLateCompletion(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	schedule := PromptSchedule{ID: "running", Directory: "/repo", RemoteID: "local", Prompt: "go", RunAt: 1000, State: "scheduled", TimingType: "interval", IntervalMinutes: 10, Timezone: "UTC", Enabled: true, SessionMode: "fresh", CreatedAt: 1, UpdatedAt: 1}
	if err := db.CreatePromptSchedule(t.Context(), schedule); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := db.ClaimPromptSchedule(t.Context(), schedule.ID, 1000, true); err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	canceled, ok, err := db.CancelPromptSchedule(t.Context(), schedule.ID, 1100)
	if err != nil || !ok || canceled.State != "canceled" {
		t.Fatalf("cancel=%+v ok=%v err=%v", canceled, ok, err)
	}
	if err := db.CompletePromptSchedule(t.Context(), schedule.ID, 2000, 1200); err == nil {
		t.Fatal("late completion succeeded")
	}
	got, _ := db.GetPromptSchedule(t.Context(), schedule.ID)
	if got.State != "canceled" {
		t.Fatalf("schedule=%+v", got)
	}
}

func TestDisableRunningPromptSchedulePreventsLateReschedule(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	schedule := PromptSchedule{ID: "running", Directory: "/repo", RemoteID: "local", Prompt: "go", RunAt: 1000, State: "scheduled", TimingType: "interval", IntervalMinutes: 10, Timezone: "UTC", Enabled: true, SessionMode: "fresh", CreatedAt: 1, UpdatedAt: 1}
	if err := db.CreatePromptSchedule(t.Context(), schedule); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := db.ClaimPromptSchedule(t.Context(), schedule.ID, 1000, true); err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	disabled, err := db.SetPromptScheduleEnabled(t.Context(), schedule.ID, false, schedule.RunAt, 1100)
	if err != nil || disabled.Enabled || disabled.State != "scheduled" {
		t.Fatalf("disabled=%+v err=%v", disabled, err)
	}
	if err := db.ReschedulePromptSchedule(t.Context(), schedule.ID, 2000, "late", 1200); err == nil {
		t.Fatal("late reschedule succeeded")
	}
	got, _ := db.GetPromptSchedule(t.Context(), schedule.ID)
	if got.State != "scheduled" || got.Enabled {
		t.Fatalf("schedule=%+v", got)
	}
}

func TestPromptScheduleV40MigrationDefaultsExistingRows(t *testing.T) {
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if err := ensureSchemaVersionTable(raw); err != nil {
		t.Fatal(err)
	}
	tx, err := raw.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for version := 1; version <= 39; version++ {
		if err := applyMigration(tx, version); err != nil {
			t.Fatalf("migrate v%d: %v", version, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_version (version, applied_at) VALUES (?, 0)`, version); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO prompt_schedule (id, directory, prompt, run_at, state, created_at, updated_at) VALUES ('old', '/repo', 'go', 1000, 'scheduled', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := migrate(raw); err != nil {
		t.Fatal(err)
	}
	db := &DB{db: raw}
	got, err := db.GetPromptSchedule(t.Context(), "old")
	if err != nil || got.TimingType != "once" || got.Timezone != "UTC" || !got.Enabled || got.SessionMode != "fresh" {
		t.Fatalf("schedule=%+v err=%v", got, err)
	}
}
