package state

import (
	"fmt"
	"strconv"
	"testing"
	"time"
)

func TestInitialImportArchiveAtomicAndPlatformScoped(t *testing.T) {
	d := newTestDB(t)
	ctx := t.Context()
	rows := []InitialArchiveSession{{ID: "old", TimeUpdated: 123}}
	// Fail after inserting the marker. The whole transaction must roll back.
	if _, err := d.db.ExecContext(ctx, `CREATE TRIGGER fail_initial BEFORE INSERT ON archived_session BEGIN SELECT RAISE(ABORT, 'test'); END`); err != nil {
		t.Fatal(err)
	}
	if err := d.ArchiveInitialSessions(ctx, "opencode", rows); err == nil {
		t.Fatal("expected write failure")
	}
	if _, ok, err := d.GetSetting(ctx, initialImportArchiveKey("opencode")); err != nil || ok {
		t.Fatal("failed pass persisted marker", err)
	}
	if _, err := d.db.ExecContext(ctx, `DROP TRIGGER fail_initial`); err != nil {
		t.Fatal(err)
	}
	for _, platform := range []string{"opencode", "claude", "codex"} {
		if err := d.ArchiveInitialSessions(ctx, platform, rows); err != nil {
			t.Fatal(err)
		}
		if recent, err := d.InitialImportArchiveRecent(ctx, platform, time.Now().Add(-time.Hour).UnixMilli()); err != nil || !recent {
			t.Fatal("missing recent marker", err)
		}
	}
	archived, err := d.ArchivedSessions(ctx)
	if err != nil || len(archived) != 3 {
		t.Fatal("platforms not independent", archived, err)
	}
	if err := d.UnarchiveSession(ctx, "opencode", "old"); err != nil {
		t.Fatal(err)
	}
	if err := d.ArchiveInitialSessions(ctx, "opencode", append(rows, InitialArchiveSession{ID: "later"})); err != nil {
		t.Fatal(err)
	}
	archived, _ = d.ArchivedSessions(ctx)
	if len(archived) != 2 {
		t.Fatal("cleanup repeated", archived)
	}
	if err := d.SetSetting(ctx, initialImportArchiveKey("claude"), strconv.FormatInt(time.Now().Add(-48*time.Hour).UnixMilli(), 10)); err != nil {
		t.Fatal(err)
	}
	if recent, err := d.InitialImportArchiveRecent(ctx, "claude", time.Now().Add(-24*time.Hour).UnixMilli()); err != nil || recent {
		t.Fatal("old marker still recent", err)
	}
	if recent, err := d.InitialImportArchiveRecent(ctx, "missing", 0); err != nil || recent {
		t.Fatal("missing marker recent", err)
	}
}

func TestInitialImportProtectsManualState(t *testing.T) {
	d := newTestDB(t)
	ctx := t.Context()
	if err := d.PinSession(ctx, "codex", "pinned"); err != nil {
		t.Fatal(err)
	}
	if err := d.UnarchiveSession(ctx, "codex", "restored"); err != nil {
		t.Fatal(err)
	}
	if err := d.ArchiveSession(ctx, "codex", "archived", 99); err != nil {
		t.Fatal(err)
	}
	var rows []InitialArchiveSession
	for _, id := range []string{"pinned", "restored", "archived", "plain"} {
		rows = append(rows, InitialArchiveSession{ID: id, TimeUpdated: 10})
	}
	if err := d.ArchiveInitialSessions(ctx, "codex", rows); err != nil {
		t.Fatal(err)
	}
	archived, err := d.ArchivedSessions(ctx)
	if err != nil || len(archived) != 2 || archived[Key{Platform: "codex", SessionID: "archived"}] != 99 {
		t.Fatal(fmt.Sprint(archived), err)
	}
}
