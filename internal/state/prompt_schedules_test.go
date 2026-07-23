package state

import "testing"

func TestPromptSchedulePersistenceAndAtomicClaim(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	schedule := PromptSchedule{ID: "ps_1", Directory: "/repo", RemoteID: "local", Prompt: "exact\n", RunAt: 1000, State: "scheduled", CreatedAt: 500, UpdatedAt: 500}
	if err := db.CreatePromptSchedule(schedule); err != nil {
		t.Fatal(err)
	}

	if _, ok, err := db.ClaimPromptSchedule(schedule.ID, 1000, false); err != nil || !ok {
		t.Fatalf("first claim: ok=%v err=%v", ok, err)
	}
	if _, ok, err := db.ClaimPromptSchedule(schedule.ID, 1000, false); err != nil || ok {
		t.Fatalf("duplicate claim: ok=%v err=%v", ok, err)
	}
	if err := db.LinkPromptScheduleSession(schedule.ID, "opencode", "ses_1", 1100); err != nil {
		t.Fatal(err)
	}
	if err := db.FinishPromptSchedule(schedule.ID, "completed", "", 1200); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetPromptSchedule(schedule.ID)
	if err != nil || got.Prompt != "exact\n" || got.SessionID != "ses_1" || got.State != "completed" {
		t.Fatalf("schedule=%+v err=%v", got, err)
	}
}

func TestPromptScheduleClaimsDueRowsOneAtATimeAndCancel(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	for _, schedule := range []PromptSchedule{
		{ID: "due", Directory: "/a", RemoteID: "local", Prompt: "a", RunAt: 1000, State: "scheduled", CreatedAt: 1, UpdatedAt: 1},
		{ID: "later", Directory: "/a", RemoteID: "local", Prompt: "b", RunAt: 3000, State: "scheduled", CreatedAt: 2, UpdatedAt: 2},
		{ID: "other", Directory: "/b", RemoteID: "local", Prompt: "c", RunAt: 1000, State: "scheduled", CreatedAt: 3, UpdatedAt: 3},
	} {
		if err := db.CreatePromptSchedule(schedule); err != nil {
			t.Fatal(err)
		}
	}
	first, ok, err := db.ClaimNextDuePromptSchedule(2000)
	if err != nil || !ok || first.ID != "due" {
		t.Fatalf("first=%+v ok=%v err=%v", first, ok, err)
	}
	second, ok, err := db.ClaimNextDuePromptSchedule(2000)
	if err != nil || !ok || second.ID != "other" {
		t.Fatalf("second=%+v ok=%v err=%v", second, ok, err)
	}
	if _, ok, err := db.ClaimNextDuePromptSchedule(2000); err != nil || ok {
		t.Fatalf("third claim: ok=%v err=%v", ok, err)
	}
	listed, err := db.ListPromptSchedules("/a", "local")
	if err != nil || len(listed) != 2 {
		t.Fatalf("listed=%+v err=%v", listed, err)
	}
	canceled, ok, err := db.CancelPromptSchedule("later", 2100)
	if err != nil || !ok || canceled.State != "canceled" {
		t.Fatalf("canceled=%+v ok=%v err=%v", canceled, ok, err)
	}
}

func TestPromptScheduleRecoveryFailsRunningOnly(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	for _, schedule := range []PromptSchedule{
		{ID: "running", Directory: "/a", RemoteID: "local", Prompt: "a", RunAt: 1, State: "scheduled", CreatedAt: 1, UpdatedAt: 1},
		{ID: "waiting", Directory: "/a", RemoteID: "local", Prompt: "b", RunAt: 2, State: "scheduled", CreatedAt: 1, UpdatedAt: 1},
	} {
		if err := db.CreatePromptSchedule(schedule); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok, err := db.ClaimPromptSchedule("running", 3, true); err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	if err := db.FailRunningPromptSchedules(4, "interrupted"); err != nil {
		t.Fatal(err)
	}
	running, _ := db.GetPromptSchedule("running")
	waiting, _ := db.GetPromptSchedule("waiting")
	if running.State != "failed" || running.Error != "interrupted" || waiting.State != "scheduled" {
		t.Fatalf("running=%+v waiting=%+v", running, waiting)
	}
}
