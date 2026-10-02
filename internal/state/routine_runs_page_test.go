package state

import (
	"fmt"
	"testing"
)

var testOccurrence int64

func claimTestRun(t *testing.T, db *DB, routineID, id string, createdAt int64) {
	t.Helper()
	testOccurrence++
	if _, claimed, err := db.ClaimRoutineRun(t.Context(), RoutineRun{ID: id, RoutineID: routineID, Trigger: "ui", State: "running", OccurrenceAt: testOccurrence, CreatedAt: createdAt}); err != nil || !claimed {
		t.Fatalf("claim %s: claimed=%v err=%v", id, claimed, err)
	}
}

func TestListRoutineRunsPageIsBoundedOrderedAndReachesOlderRuns(t *testing.T) {
	db := openTestStateDB(t)
	if err := db.CreateRoutine(t.Context(), testRoutine("r", "R", 1)); err != nil {
		t.Fatal(err)
	}
	// Seven runs; two share created_at so the id tiebreak is exercised.
	for i, at := range []int64{10, 20, 30, 30, 40, 50, 60} {
		claimTestRun(t, db, "r", fmt.Sprintf("run-%d", i), at)
	}
	all, err := db.ListRoutineRuns(t.Context(), "r")
	if err != nil {
		t.Fatal(err)
	}
	var paged []RoutineRun
	var cursor RoutineRunCursor
	for pages := 0; ; pages++ {
		page, err := db.ListRoutineRunsPage(t.Context(), "r", 3, cursor)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) > 3 || pages > 3 {
			t.Fatalf("page %d unbounded: %d runs", pages, len(page))
		}
		if len(page) == 0 {
			break
		}
		paged = append(paged, page...)
		last := page[len(page)-1]
		cursor = RoutineRunCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	if len(paged) != len(all) {
		t.Fatalf("paged %d runs, want %d", len(paged), len(all))
	}
	for i := range all {
		if paged[i].ID != all[i].ID {
			t.Fatalf("run %d = %s, want %s (order must match full history)", i, paged[i].ID, all[i].ID)
		}
	}
	if paged[0].ID != "run-6" || paged[3].ID != "run-3" || paged[4].ID != "run-2" {
		t.Fatalf("order = %v", []string{paged[0].ID, paged[3].ID, paged[4].ID})
	}
}

func TestLatestRoutineRunsPicksNewestPerLiveRoutine(t *testing.T) {
	db := openTestStateDB(t)
	for _, id := range []string{"a", "b", "empty", "gone"} {
		if err := db.CreateRoutine(t.Context(), testRoutine(id, id, 1)); err != nil {
			t.Fatal(err)
		}
	}
	claimTestRun(t, db, "a", "a-old", 10)
	claimTestRun(t, db, "a", "a-new", 20)
	claimTestRun(t, db, "b", "b-1", 30)
	claimTestRun(t, db, "b", "b-2", 30) // same created_at: higher id wins, like history order
	claimTestRun(t, db, "gone", "gone-1", 40)
	if err := db.UpdateRoutineRun(t.Context(), RoutineRun{ID: "a-new", State: "failure", Error: "boom", FinishedAt: 25}); err != nil {
		t.Fatal(err)
	}
	if err := db.SoftDeleteRoutine(t.Context(), "gone", 50); err != nil {
		t.Fatal(err)
	}

	latest, err := db.LatestRoutineRuns(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(latest) != 2 || latest["a"].ID != "a-new" || latest["a"].State != "failure" || latest["a"].Error != "boom" || latest["b"].ID != "b-2" {
		t.Fatalf("latest = %+v", latest)
	}
	if _, ok := latest["empty"]; ok {
		t.Fatal("routine without runs has a latest run")
	}
}
