package state

import (
	"path/filepath"
	"testing"
)

// A recorded AI approval is never replaced by a later user observation of
// the same reply (a replay after restart); an AI record replaces anything.
func TestAIApprovalIsNotOverwrittenByUser(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	record := func(id string, by ApprovalActor) {
		if err := d.RecordApprovedPermission(t.Context(), "claudecode", "s", ApprovedPermission{PermissionID: id, PermissionText: "bash", ApprovedBy: by}); err != nil {
			t.Fatal(err)
		}
	}
	record("p1", ApprovalActorAI)
	record("p1", ApprovalActorUser)
	record("p2", ApprovalActorUser)
	record("p2", ApprovalActorAI)
	rows, err := d.ListApprovedPermissions(t.Context(), "claudecode", "s")
	if err != nil || len(rows) != 2 || rows[0].ApprovedBy != ApprovalActorAI || rows[1].ApprovedBy != ApprovalActorAI {
		t.Fatalf("rows = %+v %v", rows, err)
	}
}

func TestApprovedPermissionLookups(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	for _, row := range []struct{ platform, session, id string }{{"claudecode", "a", "p1"}, {"claudecode", "b", "p2"}, {"opencode", "a", "p3"}} {
		if err := d.RecordApprovedPermission(t.Context(), row.platform, row.session, ApprovedPermission{PermissionID: row.id, PermissionText: "bash"}); err != nil {
			t.Fatal(err)
		}
	}
	if p, ok, err := d.ApprovedPermission(t.Context(), "claudecode", "b", "p2"); err != nil || !ok || p.ApprovedBy != ApprovalActorAI {
		t.Fatalf("lookup = %+v %v %v", p, ok, err)
	}
	if _, ok, err := d.ApprovedPermission(t.Context(), "claudecode", "a", "p2"); err != nil || ok {
		t.Fatalf("another session's approval found: %v %v", ok, err)
	}
}
