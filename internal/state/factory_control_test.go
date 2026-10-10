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
	epic, err := db.CreateFactoryEpic(ctx, "", "Epic", "", "/repo", "", nativeTracerFormula(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := db.GetFactoryControlSession(ctx, epic.ID); err != nil || found {
		t.Fatalf("before = %v, %v", found, err)
	}
	// Replacing keeps one session per Epic.
	for _, id := range []string{"ses-1", "ses-2"} {
		if err := db.SetFactoryControlSession(ctx, FactoryControlSession{EpicID: epic.ID, Platform: "opencode", SessionID: id}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	want := FactoryControlSession{EpicID: epic.ID, Platform: "opencode", SessionID: "ses-2"}
	if got, found, err := db.GetFactoryControlSession(ctx, epic.ID); err != nil || !found || got != want {
		t.Fatalf("control = %#v, %v, %v", got, found, err)
	}
	if err := db.CloseFactoryEpic(ctx, epic.ID, true); err != nil {
		t.Fatal(err)
	}
	archived, err := db.ArchivedSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := archived[Key{Platform: "opencode", SessionID: "ses-2"}]; !ok {
		t.Fatalf("closing the Epic did not archive its control session: %v", archived)
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
