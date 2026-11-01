package state

import (
	"context"
	"testing"
	"time"
)

// A long write transaction holds state.db's only writer connection. Read-only
// methods must use the separate read pool and observe the last committed
// snapshot instead of queueing behind the writer.
func TestReadsProceedWhileWriteTransactionIsOpen(t *testing.T) {
	database := openTestStateDB(t)
	ctx := t.Context()
	if err := database.SetSetting(ctx, "committed", "yes"); err != nil {
		t.Fatal(err)
	}
	if err := database.PinSession(ctx, "opencode", "pinned"); err != nil {
		t.Fatal(err)
	}

	tx, err := database.SQL().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO setting (key, value, updated_at) VALUES ('uncommitted', 'no', 1)`); err != nil {
		t.Fatal(err)
	}

	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	value, ok, err := database.GetSetting(readCtx, "committed")
	if err != nil || !ok || value != "yes" {
		t.Fatalf("GetSetting during write transaction = %q, %v, %v", value, ok, err)
	}
	if _, ok, err := database.GetSetting(readCtx, "uncommitted"); err != nil || ok {
		t.Fatalf("read pool observed an uncommitted write: ok=%v err=%v", ok, err)
	}
	flags, err := database.SidebarFlags(readCtx)
	if err != nil || flags.Pinned[k("opencode", "pinned")] == 0 {
		t.Fatalf("SidebarFlags during write transaction = %+v, %v", flags.Pinned, err)
	}
	if _, err := database.ListInboxItems(readCtx); err != nil {
		t.Fatalf("ListInboxItems during write transaction: %v", err)
	}
	if _, err := database.PermissionApprovalStats(readCtx, 0, ""); err != nil {
		t.Fatalf("PermissionApprovalStats during write transaction: %v", err)
	}
	if _, err := database.SnoozedSessions(readCtx); err != nil {
		t.Fatalf("SnoozedSessions during write transaction: %v", err)
	}
	if _, err := database.PinnedSessions(readCtx); err != nil {
		t.Fatalf("PinnedSessions during write transaction: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if value, ok, err := database.GetSetting(ctx, "uncommitted"); err != nil || !ok || value != "no" {
		t.Fatalf("read after commit = %q, %v, %v; want the committed write", value, ok, err)
	}
}

// The read pool must refuse writes so a method mistakenly routed to it fails
// loudly instead of bypassing the single-writer connection.
func TestReadPoolIsReadOnly(t *testing.T) {
	database := openTestStateDB(t)
	if _, err := database.reader().ExecContext(t.Context(), `INSERT INTO setting (key, value, updated_at) VALUES ('x', 'y', 1)`); err == nil {
		t.Fatal("write through the read pool succeeded")
	}
}
