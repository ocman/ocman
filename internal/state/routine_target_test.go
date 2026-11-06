package state

import (
	"errors"
	"testing"
)

func TestRoutineTargetClaimCannotOverrideProjectOrOwner(t *testing.T) {
	db := openTestStateDB(t)
	routine := Routine{ID: "target", Name: "target", Directory: "/fixed", RemoteID: "local", SessionMode: "new", ScheduleKind: "none", ScheduleConfigJSON: "{}", Enabled: true}
	if err := db.CreateRoutine(t.Context(), routine); err != nil {
		t.Fatal(err)
	}
	for _, target := range []RoutineRunTarget{{Directory: "/different", RemoteID: "local"}, {Directory: "/fixed", RemoteID: "old-owner"}} {
		_, claimed, err := db.ClaimRoutineRun(t.Context(), RoutineRun{ID: "run", RoutineID: routine.ID, State: "running", Trigger: "manual", OccurrenceAt: 1}, target)
		if claimed || !errors.Is(err, ErrRoutineProject) {
			t.Fatalf("target %+v: claimed=%v, %v", target, claimed, err)
		}
	}
	routine.Directory = ""
	if err := db.UpdateRoutine(t.Context(), routine); err != nil {
		t.Fatal(err)
	}
	run, claimed, err := db.ClaimRoutineRun(t.Context(), RoutineRun{ID: "run", RoutineID: routine.ID, State: "running", Trigger: "manual", OccurrenceAt: 1}, RoutineRunTarget{Directory: "/chosen", RemoteID: "local"})
	if err != nil || !claimed || run.Directory != "/chosen" {
		t.Fatalf("claim: %+v, %v, %v", run, claimed, err)
	}
	stored, err := db.GetRoutine(t.Context(), routine.ID)
	if err != nil || stored.Directory != "" {
		t.Fatalf("definition changed: %+v, %v", stored, err)
	}
}
