package state

import (
	"database/sql"
	"errors"
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

func TestRoutineMissingRows(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	if _, err := db.GetRoutine(t.Context(), "missing"); !errors.Is(err, ErrRoutineNotFound) {
		t.Fatalf("GetRoutine error = %v", err)
	}
	if err := db.UpdateRoutine(t.Context(), testRoutine("missing", "Missing", 1)); !errors.Is(err, ErrRoutineNotFound) {
		t.Fatalf("UpdateRoutine error = %v", err)
	}
	if err := db.SoftDeleteRoutine(t.Context(), "missing", 2); !errors.Is(err, ErrRoutineNotFound) {
		t.Fatalf("SoftDeleteRoutine error = %v", err)
	}
	if _, _, err := db.ClaimRoutineRun(t.Context(), RoutineRun{ID: "run", RoutineID: "missing"}); !errors.Is(err, ErrRoutineNotFound) {
		t.Fatalf("ClaimRoutineRun error = %v", err)
	}
	if _, err := db.GetRoutineRun(t.Context(), "missing"); !errors.Is(err, ErrRoutineRunNotFound) {
		t.Fatalf("GetRoutineRun error = %v", err)
	}
	if err := db.UpdateRoutineRun(t.Context(), RoutineRun{ID: "missing"}); !errors.Is(err, ErrRoutineRunNotFound) {
		t.Fatalf("UpdateRoutineRun error = %v", err)
	}
	if err := db.LinkRoutineRun(t.Context(), "missing", "opencode", "session", 3); !errors.Is(err, ErrRoutineRunNotFound) {
		t.Fatalf("LinkRoutineRun error = %v", err)
	}
}

func TestRoutineDueRunningLinkAndIdempotentFinish(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	routine := testRoutine("routine", "Routine", 1)
	if err := db.CreateRoutine(t.Context(), routine); err != nil {
		t.Fatal(err)
	}
	if due, err := db.ListDueRoutines(t.Context(), 99); err != nil || len(due) != 0 {
		t.Fatalf("due before deadline = %+v, %v", due, err)
	}
	if due, err := db.ListDueRoutines(t.Context(), 100); err != nil || len(due) != 1 || due[0].ID != routine.ID {
		t.Fatalf("due at deadline = %+v, %v", due, err)
	}

	run, claimed, err := db.ClaimRoutineRun(t.Context(), RoutineRun{
		ID: "run", RoutineID: routine.ID, Trigger: "schedule", State: "running", OccurrenceAt: 100, CreatedAt: 2,
	})
	if err != nil || !claimed {
		t.Fatalf("claim = %+v, %v, %v", run, claimed, err)
	}
	if err := db.LinkRoutineRun(t.Context(), run.ID, "opencode", "session", 3); err != nil {
		t.Fatal(err)
	}
	if got, err := db.GetRoutineRun(t.Context(), run.ID); err != nil || got.Platform != "opencode" || got.SessionID != "session" || got.StartedAt != 3 {
		t.Fatalf("linked run = %+v, %v", got, err)
	}
	if running, err := db.ListRunningRoutineRuns(t.Context()); err != nil || len(running) != 1 || running[0].ID != run.ID {
		t.Fatalf("running = %+v, %v", running, err)
	}

	finished, err := db.FinishRoutineRun(t.Context(), run.ID, "failure", "interrupted", 4, 200, true)
	if err != nil || !finished {
		t.Fatalf("finish = %v, %v", finished, err)
	}
	finished, err = db.FinishRoutineRun(t.Context(), run.ID, "success", "", 5, 300, true)
	if err != nil || finished {
		t.Fatalf("second finish = %v, %v", finished, err)
	}
	got, err := db.GetRoutine(t.Context(), routine.ID)
	if err != nil || got.NextDueAt != 200 || !got.Enabled || got.UpdatedAt != 4 {
		t.Fatalf("routine after finish = %+v, %v", got, err)
	}
	if running, err := db.ListRunningRoutineRuns(t.Context()); err != nil || len(running) != 0 {
		t.Fatalf("running after finish = %+v, %v", running, err)
	}
}

func TestRoutineStoreErrorsAfterClose(t *testing.T) {
	db := openTestStateDB(t)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	routine := testRoutine("routine", "Routine", 1)
	run := RoutineRun{ID: "run", RoutineID: routine.ID}
	checks := []struct {
		name string
		call func() error
	}{
		{"create routine", func() error { return db.CreateRoutine(t.Context(), routine) }},
		{"get routine", func() error { _, err := db.GetRoutine(t.Context(), routine.ID); return err }},
		{"list routines", func() error { _, err := db.ListRoutines(t.Context(), false); return err }},
		{"update routine", func() error { return db.UpdateRoutine(t.Context(), routine) }},
		{"delete routine", func() error { return db.SoftDeleteRoutine(t.Context(), routine.ID, 2) }},
		{"claim run", func() error { _, _, err := db.ClaimRoutineRun(t.Context(), run); return err }},
		{"get run", func() error { _, err := db.GetRoutineRun(t.Context(), run.ID); return err }},
		{"list runs", func() error { _, err := db.ListRoutineRuns(t.Context(), routine.ID); return err }},
		{"update run", func() error { return db.UpdateRoutineRun(t.Context(), run) }},
		{"list due", func() error { _, err := db.ListDueRoutines(t.Context(), 2); return err }},
		{"list running", func() error { _, err := db.ListRunningRoutineRuns(t.Context()); return err }},
		{"link run", func() error { return db.LinkRoutineRun(t.Context(), run.ID, "opencode", "session", 2) }},
		{"finish run", func() error {
			_, err := db.FinishRoutineRun(t.Context(), run.ID, "failure", "", 2, 0, false)
			return err
		}},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.call(); err == nil {
				t.Fatal("operation succeeded on closed store")
			}
		})
	}
}

func TestRoutineRunWriteFailuresRollback(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	routine := testRoutine("routine", "Routine", 1)
	if err := db.CreateRoutine(t.Context(), routine); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.ClaimRoutineRun(t.Context(), RoutineRun{ID: "invalid", RoutineID: routine.ID, OccurrenceAt: 0}); err == nil {
		t.Fatal("claimed an invalid occurrence")
	}

	claim := func(id string, occurrence int64) RoutineRun {
		t.Helper()
		run, claimed, err := db.ClaimRoutineRun(t.Context(), RoutineRun{
			ID: id, RoutineID: routine.ID, Trigger: "schedule", State: "running", OccurrenceAt: occurrence, CreatedAt: occurrence,
		})
		if err != nil || !claimed {
			t.Fatalf("claim %s = %+v, %v, %v", id, run, claimed, err)
		}
		return run
	}
	run := claim("run-update-fails", 1)
	if _, err := db.db.Exec(`CREATE TRIGGER reject_run_finish BEFORE UPDATE ON routine_run BEGIN SELECT RAISE(ABORT, 'reject finish'); END`); err != nil {
		t.Fatal(err)
	}
	if finished, err := db.FinishRoutineRun(t.Context(), run.ID, "failure", "", 2, 0, false); err == nil || finished {
		t.Fatalf("finish with rejected run update = %v, %v", finished, err)
	}
	if _, err := db.db.Exec(`DROP TRIGGER reject_run_finish`); err != nil {
		t.Fatal(err)
	}

	run = claim("routine-update-fails", 2)
	if _, err := db.db.Exec(`CREATE TRIGGER reject_routine_advance BEFORE UPDATE ON routine BEGIN SELECT RAISE(ABORT, 'reject advance'); END`); err != nil {
		t.Fatal(err)
	}
	for _, runState := range []string{"success", "failure"} {
		if finished, err := db.FinishRoutineRun(t.Context(), run.ID, runState, "", 3, 0, false); err == nil || finished {
			t.Fatalf("%s with rejected routine update = %v, %v", runState, finished, err)
		}
	}
	got, err := db.GetRoutineRun(t.Context(), run.ID)
	if err != nil || got.State != "running" {
		t.Fatalf("run after rollback = %+v, %v", got, err)
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
