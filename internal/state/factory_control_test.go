package state

import (
	"encoding/json"
	"testing"
	"time"
)

func TestFactoryControlSessionLifecycle(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	ctx := t.Context()
	epic, err := db.CreateFactoryEpicWithProjectsAndPermissionRules(ctx, "", "Epic", "", "/repo", "", nativeTracerFormula(t), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := db.GetFactoryControlSession(ctx, epic.ID); err != nil || found {
		t.Fatalf("before = %v, %v", found, err)
	}
	// Replacing keeps one session per Epic.
	for _, id := range []string{"ses-1", "ses-2"} {
		if adopted, err := db.SetFactoryControlSession(ctx, FactoryControlSession{EpicID: epic.ID, Platform: "opencode", SessionID: id}, time.Now()); err != nil || !adopted {
			t.Fatal(err)
		}
	}
	want := FactoryControlSession{EpicID: epic.ID, Platform: "opencode", SessionID: "ses-2"}
	if got, found, err := db.GetFactoryControlSession(ctx, epic.ID); err != nil || !found || got != want {
		t.Fatalf("control = %#v, %v, %v", got, found, err)
	}
	until := time.Now().Add(time.Hour).UnixMilli()
	if err := db.SnoozeSession(ctx, "opencode", "ses-2", until); err != nil {
		t.Fatal(err)
	}
	if err := db.CloseFactoryEpic(ctx, epic.ID, true); err != nil {
		t.Fatal(err)
	}
	if keys, err := db.WakeSnoozedSessions(ctx, until); err != nil || len(keys) != 0 {
		t.Fatalf("closed Factory session woke: %v %v", keys, err)
	}
	archived, err := db.ArchivedSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := archived[Key{Platform: "opencode", SessionID: "ses-2"}]; !ok {
		t.Fatalf("closing the Epic did not archive its control session: %v", archived)
	}
	// A session launched while the Epic closed is never adopted.
	if adopted, err := db.SetFactoryControlSession(ctx, FactoryControlSession{EpicID: epic.ID, Platform: "opencode", SessionID: "ses-3"}, time.Now()); err != nil || adopted {
		t.Fatalf("closed epic adopted = %v, %v", adopted, err)
	}
	if got, _, _ := db.GetFactoryControlSession(ctx, epic.ID); got.SessionID != "ses-2" {
		t.Fatalf("control after close = %#v", got)
	}
}

func TestFactoryControlActionRunsOnce(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	ctx := t.Context()
	action := FactoryControlAction{ID: "fca_1", EpicID: "epic-1", Action: "reopen", Args: json.RawMessage(`{"issue_id":"epic-1.3"}`), Summary: "Run tests again"}
	if err := db.CreateFactoryControlAction(ctx, action, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := db.GetFactoryControlAction(ctx, "epic-2", "fca_1"); found {
		t.Fatal("another Epic read the proposal")
	}
	got, found, err := db.GetFactoryControlAction(ctx, "epic-1", "fca_1")
	if err != nil || !found || got.Status != "pending" || got.Summary != "Run tests again" || string(got.Args) != `{"issue_id":"epic-1.3"}` {
		t.Fatalf("proposal = %#v, %v, %v", got, found, err)
	}
	if won, err := db.TransitionFactoryControlAction(ctx, "epic-1", "fca_1", "pending", "confirmed", ""); err != nil || !won {
		t.Fatalf("confirm = %v, %v", won, err)
	}
	if won, _ := db.TransitionFactoryControlAction(ctx, "epic-1", "fca_1", "pending", "confirmed", ""); won {
		t.Fatal("a proposal was confirmed twice")
	}
	if won, _ := db.TransitionFactoryControlAction(ctx, "epic-1", "fca_1", "confirmed", "done", "ok"); !won {
		t.Fatal("finishing failed")
	}
	if got, _, _ := db.GetFactoryControlAction(ctx, "epic-1", "fca_1"); got.Status != "done" || got.Result != "ok" {
		t.Fatalf("finished = %#v", got)
	}
}

func TestFactoryControlAlternativesAreEpicScoped(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	for _, action := range []FactoryControlAction{{ID: "chosen", EpicID: "one"}, {ID: "alternative", EpicID: "one"}, {ID: "other", EpicID: "two"}} {
		if err := db.CreateFactoryControlAction(t.Context(), action, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.DismissFactoryControlAlternatives(t.Context(), "one", "chosen"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ epic, id, status string }{{"one", "chosen", "pending"}, {"one", "alternative", "dismissed"}, {"two", "other", "pending"}} {
		action, _, err := db.GetFactoryControlAction(t.Context(), tc.epic, tc.id)
		if err != nil || action.Status != tc.status {
			t.Fatalf("%s = %#v, %v", tc.id, action, err)
		}
	}
}
