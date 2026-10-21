package state

import "testing"

func TestSessionRekeyPreservesClaimedRoutineSnapshotImmutability(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "rollback"}[conflict], func(t *testing.T) {
			d := openTestStateDB(t)
			r := testRoutine("routine", "existing", 1)
			r.Platform = "test"
			r.SessionID = "native"
			r.SessionMode = "existing"
			if err := d.CreateRoutine(t.Context(), r); err != nil {
				t.Fatal(err)
			}
			run, claimed, err := d.ClaimRoutineRun(t.Context(), RoutineRun{ID: "claimed", RoutineID: r.ID, State: "running", Trigger: "manual", OccurrenceAt: 1, CreatedAt: 1})
			if err != nil || !claimed {
				t.Fatalf("claim: %+v %v", run, err)
			}
			if conflict {
				for _, id := range []string{"native", "ocm_canonical"} {
					if err := d.PinSession(t.Context(), "test", id); err != nil {
						t.Fatal(err)
					}
				}
			}
			err = d.RekeySessionReferences(t.Context(), "test", map[string]string{"native": "ocm_canonical"})
			if (err != nil) != conflict {
				t.Fatalf("rekey conflict=%v: %v", conflict, err)
			}
			stored, err := d.GetRoutineRun(t.Context(), run.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := "ocm_canonical"
			if conflict {
				want = "native"
			}
			if stored.TargetSessionID != want || stored.Prompt != run.Prompt {
				t.Fatalf("snapshot changed: %+v", stored)
			}
			if _, err := d.db.Exec(`UPDATE routine_run SET target_session_id='forbidden' WHERE id='claimed'`); err == nil {
				t.Fatal("snapshot trigger was not restored")
			}
			if _, err := d.db.Exec(`UPDATE routine_run SET prompt='forbidden' WHERE id='claimed'`); err == nil {
				t.Fatal("snapshot prompt became mutable")
			}
		})
	}
}
