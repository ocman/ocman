package state

import (
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

func TestPlatformMigrationPinsExistingOpenCodeWork(t *testing.T) {
	path := t.TempDir() + "/state.db"
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	formula := nativeTracerFormula(t)
	epic, err := db.CreateFactoryEpic(t.Context(), "", "Existing work", "", "/repo", "stable-key", formula)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateRoutine(t.Context(), Routine{ID: "existing", Name: "Existing", Directory: "/repo", SessionMode: "reuse", SessionID: "old-session"}); err != nil {
		t.Fatal(err)
	}
	attempt, err := db.CreatePreparedFactoryAttempt(t.Context(), epic.ID, factoryIssueID(t, db, epic.ID, "plan"), model.FactoryAttemptPolicy{Repository: "/repo", Profile: "factory-plan/v1"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`UPDATE factory_epic SET platform = ''; DELETE FROM schema_version WHERE version > 111`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.CreateFactoryEpic(t.Context(), "", "Existing work", "", "/repo", "stable-key", formula); err != nil {
		t.Fatalf("idempotent pre-upgrade create failed: %v", err)
	}
	if err := db.SetSetting(t.Context(), DefaultPlatformSetting, "fake-provider"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateFactoryEpic(t.Context(), "", "Existing work", "", "/repo", "stable-key", formula); err != nil {
		t.Fatalf("default change broke replay: %v", err)
	}
	if _, err := db.CreateFactoryEpicWithProjectsAndPermissionRules(t.Context(), "", "Existing work", "", "/repo", "stable-key", formula, nil, nil, "fake-provider"); err == nil {
		t.Fatal("explicit platform mismatch must conflict")
	}
	routine, err := db.GetRoutine(t.Context(), "existing")
	if err != nil || routine.Platform != "opencode" || routine.SessionID != "old-session" {
		t.Fatalf("legacy routine = %+v, %v", routine, err)
	}
	savedEpic, err := db.GetFactoryEpic(t.Context(), epic.ID)
	if err != nil || savedEpic.Platform != "opencode" {
		t.Fatalf("legacy epic = %+v, %v", savedEpic, err)
	}
	savedAttempt, found, err := db.GetFactoryAttempt(t.Context(), attempt.ID)
	if err != nil || !found || savedAttempt.FrozenPolicy.Platform != "opencode" {
		t.Fatalf("legacy attempt = %+v, %v", savedAttempt, err)
	}
}
