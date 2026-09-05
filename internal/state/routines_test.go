package state

import (
	"database/sql"
	"sync"
	"testing"
)

func testRoutine(id, name string, createdAt int64) Routine {
	return Routine{
		ID: id, Name: name, Prompt: "run " + name, Directory: "/repo/" + id,
		RemoteID: "local", ScheduleKind: "interval", ScheduleConfigJSON: `{"minutes":15}`,
		NextDueAt: 100, Enabled: true, DeleteAfterSuccess: true,
		CreatedAt: createdAt, UpdatedAt: createdAt,
	}
}

func TestRoutineCRUDUniquenessOrderingAndSoftDelete(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	second := testRoutine("r2", "Bravo", 2)
	first := testRoutine("r1", "alpha", 1)
	if err := db.CreateRoutine(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateRoutine(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateRoutine(t.Context(), testRoutine("duplicate", "ALPHA", 3)); err == nil {
		t.Fatal("created a second active routine with the same name")
	}

	listed, err := db.ListRoutines(t.Context(), false)
	if err != nil || len(listed) != 2 || listed[0].ID != first.ID || listed[1].ID != second.ID {
		t.Fatalf("routines = %+v, %v", listed, err)
	}
	first.Prompt = "updated prompt"
	first.ScheduleKind = "cron"
	first.ScheduleConfigJSON = `{"cron":"0 9 * * 1","timezone":"UTC"}`
	first.NextDueAt = 200
	first.Enabled = false
	first.DeleteAfterSuccess = false
	first.UpdatedAt = 4
	if err := db.UpdateRoutine(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetRoutine(t.Context(), first.ID)
	if err != nil || got.Name != first.Name || got.Prompt != first.Prompt || got.Directory != first.Directory || got.RemoteID != first.RemoteID ||
		got.ScheduleKind != "cron" || got.ScheduleConfigJSON != first.ScheduleConfigJSON || got.NextDueAt != 200 || got.Enabled ||
		got.DeleteAfterSuccess || got.CreatedAt != 1 || got.UpdatedAt != 4 {
		t.Fatalf("routine = %+v, %v", got, err)
	}

	if err := db.SoftDeleteRoutine(t.Context(), first.ID, 5); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateRoutine(t.Context(), testRoutine("replacement", "Alpha", 6)); err != nil {
		t.Fatalf("reuse deleted name: %v", err)
	}
	deleted, err := db.GetRoutine(t.Context(), first.ID)
	if err != nil || !deleted.Deleted || deleted.DeletedAt != 5 || deleted.Enabled {
		t.Fatalf("deleted routine = %+v, %v", deleted, err)
	}
	all, err := db.ListRoutines(t.Context(), true)
	if err != nil || len(all) != 3 {
		t.Fatalf("all routines = %+v, %v", all, err)
	}
}

func TestRoutineRunSnapshotsHistoryAndOrdering(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	routine := testRoutine("routine", "Original", 1)
	if err := db.CreateRoutine(t.Context(), routine); err != nil {
		t.Fatal(err)
	}

	run, claimed, err := db.ClaimRoutineRun(t.Context(), RoutineRun{
		ID: "run-1", RoutineID: routine.ID, Trigger: "schedule", State: "dispatching",
		OccurrenceAt: 100, CreatedAt: 10, StartedAt: 11,
	})
	if err != nil || !claimed || run.RoutineUpdatedAt != routine.UpdatedAt || run.RoutineName != routine.Name || run.Prompt != routine.Prompt || run.Directory != routine.Directory || run.RemoteID != routine.RemoteID {
		t.Fatalf("run = %+v, claimed=%v, err=%v", run, claimed, err)
	}
	routine.Name = "Renamed"
	routine.Prompt = "new prompt"
	routine.Directory = "/new"
	routine.RemoteID = "remote"
	routine.UpdatedAt = 20
	if err := db.UpdateRoutine(t.Context(), routine); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateRoutineRun(t.Context(), RoutineRun{ID: run.ID, Platform: "opencode", SessionID: "session", State: "dispatched", FinishedAt: 12}); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := db.ClaimRoutineRun(t.Context(), RoutineRun{ID: "run-2", RoutineID: routine.ID, Trigger: "ui", State: "dispatching", OccurrenceAt: 200, CreatedAt: 20}); err != nil || !claimed {
		t.Fatalf("second run: claimed=%v err=%v", claimed, err)
	}

	runs, err := db.ListRoutineRuns(t.Context(), routine.ID)
	if err != nil || len(runs) != 2 || runs[0].ID != "run-2" || runs[1].RoutineName != "Original" || runs[1].Prompt != "run Original" || runs[1].State != "dispatched" || runs[1].SessionID != "session" {
		t.Fatalf("runs = %+v, %v", runs, err)
	}
	if _, err := db.db.Exec(`UPDATE routine_run SET prompt = 'changed' WHERE id = 'run-1'`); err == nil {
		t.Fatal("changed a routine run snapshot")
	}
	if _, err := db.db.Exec(`DELETE FROM routine_run WHERE id = 'run-1'`); err == nil {
		t.Fatal("deleted append-only routine history")
	}
	if err := db.SoftDeleteRoutine(t.Context(), routine.ID, 30); err != nil {
		t.Fatal(err)
	}
	if runs, err = db.ListRoutineRuns(t.Context(), routine.ID); err != nil || len(runs) != 2 {
		t.Fatalf("history after delete = %+v, %v", runs, err)
	}
}

func TestRoutineRunCompetingClaims(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	db.db.SetMaxOpenConns(1)
	if err := db.CreateRoutine(t.Context(), testRoutine("routine", "Routine", 1)); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan bool, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, id := range []string{"scheduler", "run-now"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			<-start
			_, claimed, err := db.ClaimRoutineRun(t.Context(), RoutineRun{
				ID: id, RoutineID: "routine", Trigger: id, State: "dispatching", OccurrenceAt: 100, CreatedAt: 2,
			})
			results <- claimed
			errs <- err
		}(id)
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	claims := 0
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for claimed := range results {
		if claimed {
			claims++
		}
	}
	if claims != 1 {
		t.Fatalf("successful claims = %d, want 1", claims)
	}
}

func TestRoutineMigrationPreservesWorkflowHistory(t *testing.T) {
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
	for version := 1; version <= 69; version++ {
		if err := applyMigration(tx, version); err != nil {
			t.Fatalf("migrate v%d: %v", version, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_version (version, applied_at) VALUES (?, 0)`, version); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO workflow_definition (id, name, current_revision, created_at, updated_at) VALUES ('historic', 'Historic', 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := migrate(raw); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := raw.QueryRow(`SELECT name FROM workflow_definition WHERE id = 'historic'`).Scan(&name); err != nil || name != "Historic" {
		t.Fatalf("workflow history = %q, %v", name, err)
	}
	for _, table := range []string{"routine", "routine_run"} {
		if err := raw.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
			t.Fatalf("%s table: %v", table, err)
		}
	}
}
