package state

import (
	"math"
	"path/filepath"
	"testing"
	"time"
)

func TestSnoozeLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Hour).UnixMilli()
	for _, platform := range []string{"opencode", "r-box:claudecode"} {
		if err := d.SnoozeSession(t.Context(), platform, "same", until); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	archived, err := d.ArchivedSessions(t.Context())
	if err != nil || len(archived) != 2 || archived[Key{"opencode", "same"}] != math.MaxInt64 {
		t.Fatalf("archives: %v, %v", archived, err)
	}
	keys, err := d.WakeSnoozedSessions(t.Context(), until-1)
	if err != nil || len(keys) != 0 {
		t.Fatalf("early wake: %v, %v", keys, err)
	}
	keys, err = d.WakeSnoozedSessions(t.Context(), until)
	if err != nil || len(keys) != 2 {
		t.Fatalf("wake: %v, %v", keys, err)
	}
	keep, err := d.SessionsUnarchivedSince(t.Context(), until)
	if err != nil || len(keep) != 2 {
		t.Fatalf("auto-archive protection: %v, %v", keep, err)
	}
	keys, err = d.WakeSnoozedSessions(t.Context(), until+1)
	if err != nil || len(keys) != 0 {
		t.Fatalf("repeat wake: %v, %v", keys, err)
	}
}

func TestSnoozeCancelledByArchiveOrOpen(t *testing.T) {
	for _, archive := range []bool{false, true} {
		t.Run(map[bool]string{false: "open", true: "archive"}[archive], func(t *testing.T) {
			d := openTestStateDB(t)
			until := time.Now().Add(time.Hour).UnixMilli()
			if err := d.SnoozeSession(t.Context(), "opencode", "s1", until); err != nil {
				t.Fatal(err)
			}
			var err error
			if archive {
				err = d.ArchiveSession(t.Context(), "opencode", "s1", 42)
			} else {
				err = d.UnarchiveSession(t.Context(), "opencode", "s1")
			}
			if err != nil {
				t.Fatal(err)
			}
			keys, err := d.WakeSnoozedSessions(t.Context(), until)
			if err != nil || len(keys) != 0 {
				t.Fatalf("cancelled snooze woke: %v, %v", keys, err)
			}
			got, err := d.IsSessionArchived(t.Context(), "opencode", "s1")
			if err != nil || got != archive {
				t.Fatalf("archived: %v, %v", got, err)
			}
		})
	}
}

func TestSnoozeRejectsPastDeadline(t *testing.T) {
	d := openTestStateDB(t)
	if err := d.SnoozeSession(t.Context(), "opencode", "s1", time.Now().UnixMilli()-1); err == nil {
		t.Fatal("accepted past deadline")
	}
}
