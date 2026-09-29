package state

import (
	"testing"
	"time"
)

func TestFactoryAttemptLastResumedAt(t *testing.T) {
	d := openTestDB(t)
	ctx := t.Context()
	if at, err := d.FactoryAttemptLastResumedAt(ctx, "att-1"); err != nil || !at.IsZero() {
		t.Fatalf("no gates = %v, %v", at, err)
	}
	// Only the resolved_at columns matter here; skip the parent rows.
	if _, err := d.db.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`INSERT INTO factory_recovery_gate (issue_id, epic_id, attempt_id, work_item_id, question, reason, created_at, resolved_at) VALUES ('g1', 'e', 'att-1', 'w', 'q', 'r', 1, 1000)`,
		`INSERT INTO factory_authority_escalation_gate (issue_id, epic_id, attempt_id, work_item_id, request_id, permission, target, created_at, resolved_at) VALUES ('g2', 'e', 'att-1', 'w', 'req', 'p', 't', 1, 3000)`,
		`INSERT INTO factory_project_request_gate (issue_id, epic_id, attempt_id, work_item_id, requested_project, created_at, resolved_at) VALUES ('g3', 'e', 'att-1', 'w', '/p', 1, 2000)`,
		`INSERT INTO factory_recovery_gate (issue_id, epic_id, attempt_id, work_item_id, question, reason, created_at, resolved_at) VALUES ('g4', 'e', 'att-2', 'w', 'q', 'r', 1, 9000)`,
	} {
		if _, err := d.db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	if at, err := d.FactoryAttemptLastResumedAt(ctx, "att-1"); err != nil || !at.Equal(time.UnixMilli(3000)) {
		t.Fatalf("latest resume = %v, %v", at, err)
	}
}
