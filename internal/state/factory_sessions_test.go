package state

import (
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

func TestFactorySessionsMapsAttemptSessions(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	ctx := t.Context()
	at := time.UnixMilli(1_000)
	policy := model.FactoryAttemptPolicy{Repository: "/repo", Profile: "factory-implement/v1"}

	active, err := db.CreatePreparedFactoryAttempt(ctx, "epic", "work-1", policy, at)
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := db.ActivateFactoryAttempt(ctx, active.ID, model.PlanningSession{Platform: "r-host:opencode", ID: "ses-1"}, at); err != nil || !changed {
		t.Fatalf("activate = %v, %v", changed, err)
	}
	// A prepared attempt has no session yet and must not produce an entry.
	if _, err := db.CreatePreparedFactoryAttempt(ctx, "epic", "work-2", policy, at); err != nil {
		t.Fatal(err)
	}

	got, err := db.FactorySessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[Key{Platform: "r-host:opencode", SessionID: "ses-1"}] != active.ID {
		t.Fatalf("FactorySessions = %#v, want only ses-1 -> %s", got, active.ID)
	}
}
