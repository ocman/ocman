package state

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

func TestFactoryAttemptLifecycle(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	ctx := context.Background()
	policy := model.FactoryAttemptPolicy{Profile: "factory-implement/v1"}

	first, err := db.CreatePreparedFactoryAttempt(ctx, "epic-1", "work-1", policy, time.UnixMilli(1000))
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.CreatePreparedFactoryAttempt(ctx, "epic-1", "work-1", policy, time.UnixMilli(2000))
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == "" || second.ID == first.ID || first.Sequence != 1 || second.Sequence != 2 || first.Phase != model.FactoryAttemptPrepared {
		t.Fatalf("attempts = %#v, %#v", first, second)
	}

	changed, err := db.ActivateFactoryAttempt(ctx, first.ID, model.PlanningSession{Platform: "opencode", ID: "session-1"}, time.UnixMilli(3000))
	if err != nil || !changed {
		t.Fatalf("activate = %v, %v", changed, err)
	}
	if changed, err := db.ActivateFactoryAttempt(ctx, first.ID, model.PlanningSession{Platform: "opencode", ID: "session-1"}, time.UnixMilli(4000)); err != nil || changed {
		t.Fatalf("repeated activate = %v, %v", changed, err)
	}
	result := model.FactoryAttemptResult{Summary: "stub completed"}
	changed, err = db.CompleteFactoryAttempt(ctx, first.ID, result, time.UnixMilli(5000))
	if err != nil || !changed {
		t.Fatalf("complete = %v, %v", changed, err)
	}
	if changed, err := db.CompleteFactoryAttempt(ctx, first.ID, result, time.UnixMilli(6000)); err != nil || changed {
		t.Fatalf("repeated complete = %v, %v", changed, err)
	}

	got, ok, err := db.GetFactoryAttempt(ctx, first.ID)
	if err != nil || !ok || got.Phase != model.FactoryAttemptTerminal || got.Outcome != model.FactoryAttemptSucceeded || got.Result == nil || *got.Result != result || got.StartedAt != 3000 || got.FinishedAt != 5000 {
		t.Fatalf("completed attempt = %#v, %v, %v", got, ok, err)
	}
	failure := model.FactoryAttemptFailure{Type: "claim", Message: "already claimed"}
	if changed, err := db.FailFactoryAttempt(ctx, second.ID, failure, time.UnixMilli(7000)); err != nil || !changed {
		t.Fatalf("fail prepared = %v, %v", changed, err)
	}
	if changed, err := db.FailFactoryAttempt(ctx, second.ID, failure, time.UnixMilli(8000)); err != nil || changed {
		t.Fatalf("repeated fail = %v, %v", changed, err)
	}
	got, ok, err = db.GetFactoryAttempt(ctx, second.ID)
	if err != nil || !ok || got.Outcome != model.FactoryAttemptFailed || got.Failure != failure || got.FinishedAt != 7000 {
		t.Fatalf("failed attempt = %#v, %v, %v", got, ok, err)
	}

	attempts, err := db.ListFactoryAttempts(ctx, "epic-1")
	if err != nil || len(attempts) != 2 || attempts[0].ID != first.ID || attempts[1].ID != second.ID {
		t.Fatalf("attempt list = %#v, %v", attempts, err)
	}
	if _, ok, err := db.GetFactoryAttempt(ctx, "missing"); err != nil || ok {
		t.Fatalf("missing attempt = ok %v, err %v", ok, err)
	}
}

func TestFactoryAttemptDurabilityAndMonotonicSequence(t *testing.T) {
	path := t.TempDir() + "/state.db"
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	policy := model.FactoryAttemptPolicy{Profile: "factory-implement/v1"}
	first, err := db.CreatePreparedFactoryAttempt(ctx, "epic-1", "work-1", policy, time.UnixMilli(1000))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ActivateFactoryAttempt(ctx, first.ID, model.PlanningSession{}, time.UnixMilli(2000)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, ok, err := db.GetFactoryAttempt(ctx, first.ID)
	if err != nil || !ok || got.Phase != model.FactoryAttemptActive || got.FrozenPolicy != policy {
		t.Fatalf("recovered attempt = %#v, %v, %v", got, ok, err)
	}
	if changed, err := db.ActivateFactoryAttempt(ctx, first.ID, model.PlanningSession{}, time.UnixMilli(3000)); err != nil || changed {
		t.Fatalf("recovered activation = %v, %v", changed, err)
	}
	second, err := db.CreatePreparedFactoryAttempt(ctx, "epic-1", "work-1", policy, time.UnixMilli(4000))
	if err != nil || second.Sequence != 2 {
		t.Fatalf("next attempt = %#v, %v", second, err)
	}
}

func TestFactoryAuditPersistsAttemptID(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	ctx := context.Background()
	attempt, err := db.CreatePreparedFactoryAttempt(ctx, "epic-1", "work-1", model.FactoryAttemptPolicy{Profile: "factory-implement/v1"}, time.UnixMilli(1000))
	if err != nil {
		t.Fatal(err)
	}

	direct := model.AuditRecord{EpicID: "epic-1", WorkID: "work-1", AttemptID: attempt.ID, Actor: "factory", Action: "attempt.prepared", Details: map[string]bool{"claimed": false}, At: time.UnixMilli(2000)}
	if err := db.AppendFactoryAudit(ctx, direct); err != nil {
		t.Fatal(err)
	}
	once := model.AuditRecord{EpicID: "epic-1", WorkID: "work-1", AttemptID: attempt.ID, Actor: "factory", Action: "attempt.active", Details: map[string]bool{"claimed": true}, At: time.UnixMilli(3000)}
	if err := db.AppendFactoryAuditOnce(ctx, once); err != nil {
		t.Fatal(err)
	}
	if err := db.AppendFactoryAuditOnce(ctx, once); err != nil {
		t.Fatal(err)
	}

	rows, err := db.db.Query(`SELECT attempt_id FROM factory_audit_record ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != attempt.ID || ids[1] != attempt.ID {
		t.Fatalf("audit attempt ids = %#v", ids)
	}
}

func TestFactoryAttemptRejectsInvalidAndCorruptEvidence(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	ctx := context.Background()
	attempt, err := db.CreatePreparedFactoryAttempt(ctx, "epic-1", "work-1", model.FactoryAttemptPolicy{Profile: "factory-implement/v1"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ActivateFactoryAttempt(ctx, attempt.ID, model.PlanningSession{Platform: "opencode"}, time.Now()); err == nil {
		t.Fatal("activation accepted an incomplete session identity")
	}
	if _, err := db.FailFactoryAttempt(ctx, attempt.ID, model.FactoryAttemptFailure{}, time.Now()); err == nil {
		t.Fatal("failure accepted an empty type")
	}
	if _, err := db.db.Exec(`UPDATE factory_attempt SET frozen_policy_json = '{' WHERE id = ?`, attempt.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.GetFactoryAttempt(ctx, attempt.ID); err == nil || !strings.Contains(err.Error(), "decoding frozen policy") {
		t.Fatalf("corrupt policy error = %v", err)
	}

	second, err := db.CreatePreparedFactoryAttempt(ctx, "epic-1", "work-2", model.FactoryAttemptPolicy{Profile: "factory-review/v1"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := db.ActivateFactoryAttempt(ctx, second.ID, model.PlanningSession{}, time.Now()); err != nil || !changed {
		t.Fatalf("activate = %t, %v", changed, err)
	}
	if changed, err := db.CompleteFactoryAttempt(ctx, second.ID, model.FactoryAttemptResult{SchemaVersion: 1, Summary: "done"}, time.Now()); err != nil || !changed {
		t.Fatalf("complete = %t, %v", changed, err)
	}
	if _, err := db.db.Exec(`UPDATE factory_attempt SET result_json = '{' WHERE id = ?`, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.GetFactoryAttempt(ctx, second.ID); err == nil || !strings.Contains(err.Error(), "decoding result") {
		t.Fatalf("corrupt result error = %v", err)
	}
}
