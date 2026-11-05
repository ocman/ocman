package state

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestWorktreeMaintenanceLeaseAndArchiveRecords(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx, release := d.WorktreeMaintenance(t.Context())
	nested, done := d.WorktreeActivity(ctx)
	if !d.holdsWorktreeLease(nested, true) {
		t.Fatal("nested activity lost exclusive lease")
	}
	done()
	if err := d.ArchiveSession(ctx, "fake", "s", 100); err != nil {
		t.Fatal(err)
	}
	if err := d.PinSession(ctx, "fake", "s"); err != nil {
		t.Fatal(err)
	}
	if err := d.MarkSessionSeen(ctx, "fake", "s", 100); err != nil {
		t.Fatal(err)
	}
	if err := d.SnoozeSession(ctx, "fake", "snooze", time.Now().Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	rows, err := d.ArchivedSessionRecords(ctx)
	if err != nil || len(rows) != 2 {
		t.Fatalf("archive records: %+v %v", rows, err)
	}
	for _, row := range rows {
		if row.ArchivedAt <= 0 || row.Platform != "fake" {
			t.Fatalf("bad record: %+v", row)
		}
		if row.SessionID == "s" && (row.TimeUpdated != 100 || row.SnoozedUntil != 0) {
			t.Fatalf("bad archive: %+v", row)
		}
	}
	started, acquired := make(chan struct{}), make(chan struct{})
	go func() {
		close(started)
		_, finish := d.WorktreeActivity(t.Context())
		close(acquired)
		finish()
	}()
	<-started
	select {
	case <-acquired:
		t.Fatal("activity crossed cleanup fence")
	default:
	}
	release()
	release() // release is idempotent
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("activity remained blocked")
	}
	if d.holdsWorktreeLease(ctx, false) {
		t.Fatal("escaped context retained a released lease")
	}
	ctx, release = d.WorktreeActivity(ctx)
	if !d.holdsWorktreeLease(ctx, false) || d.holdsWorktreeLease(ctx, true) {
		t.Fatal("activity lease is not shared")
	}
	release()
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ArchivedSessionRecords(context.Background()); err == nil {
		t.Fatal("ignored database read failure")
	}
}

func TestNilWorktreeLease(t *testing.T) {
	var d *DB
	ctx, release := d.WorktreeActivity(t.Context())
	release()
	_, release = d.WorktreeMaintenance(ctx)
	release()
}
