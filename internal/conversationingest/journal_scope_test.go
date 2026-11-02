package conversationingest

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/historyjournal"
	"github.com/ocman/ocman/internal/platforms"
)

func TestJournalActivityDoesNotReadCompletedDescendants(t *testing.T) {
	f := bulkHistory(3)
	f.snapshots["session-1"].Session.ParentID = "session-0"
	f.snapshots["session-2"].Session.ParentID = "session-0"
	m, _ := testImporter(t, f)
	w := m.workers[0]
	w.journaled = true
	if err := w.sweepChecked(t.Context()); err != nil {
		t.Fatal(err)
	}
	before := f.reads
	if err := w.refreshJournal(t.Context(), []historyjournal.Record{{Session: "session-0", Type: "Stop"}}); err != nil {
		t.Fatal(err)
	}
	if n := f.reads - before; n != 1 {
		t.Fatalf("ordinary activity read %d sessions, want only the changed parent", n)
	}
}

type familyHistory struct {
	*fakeHistory
	family []db.HistoryEntry
	calls  int
	err    error
}

func (f *familyHistory) HistoryFamilyInventory(context.Context, string) ([]db.HistoryEntry, error) {
	f.calls++
	return f.family, f.err
}

func TestScopedDiscoveryFailuresAndFallbacks(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		entries []db.HistoryEntry
		wantErr bool
	}{
		{name: "unsupported", err: platforms.ErrUnsupported},
		{name: "missing", err: platforms.ErrNotFound},
		{name: "failure", err: errors.New("family offline"), wantErr: true},
		{name: "deferred", err: &platforms.HistoryDeferred{Count: 1}, entries: []db.HistoryEntry{{ID: "session-0", Version: "v1"}}, wantErr: true},
		{name: "empty identity", entries: []db.HistoryEntry{{}}, wantErr: true},
		{name: "parent absent from hints"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := bulkHistory(3)
			m, _ := testImporter(t, f)
			w := m.workers[0]
			w.journaled = true
			if err := w.sweepChecked(t.Context()); err != nil {
				t.Fatal(err)
			}
			w.source.Reader = &familyHistory{fakeHistory: f, family: tc.entries, err: tc.err}
			err := w.refreshJournal(t.Context(), []historyjournal.Record{{Session: "session-0", Type: "SubagentStop"}})
			if (err != nil) != tc.wantErr {
				t.Fatalf("discovery error=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestSubagentStopUsesScopedDiscovery(t *testing.T) {
	f := bulkHistory(3)
	m, _ := testImporter(t, f)
	w := m.workers[0]
	w.journaled = true
	if err := w.sweepChecked(t.Context()); err != nil {
		t.Fatal(err)
	}
	addSession(f, "child", "v1", "new child")
	f.snapshots["child"].Session.ParentID = "session-0"
	src := &familyHistory{fakeHistory: f, family: []db.HistoryEntry{f.entries[0], f.entries[3]}}
	w.source.Reader = src
	f.inventoryErr = errors.New("unrelated source inventory must not run")
	before := f.reads
	if err := w.refreshJournal(t.Context(), []historyjournal.Record{{Session: "session-0", Type: "SubagentStop"}, {Session: "child", Type: "Stop"}}); err != nil {
		t.Fatal(err)
	}
	if src.calls != 1 || f.reads-before != 2 {
		t.Fatalf("discovery calls=%d reads=%d", src.calls, f.reads-before)
	}
}
