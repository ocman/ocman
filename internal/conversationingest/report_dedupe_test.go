package conversationingest

import (
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
)

// A session that keeps failing for the same reason is retried every pass. It
// must be reported when its cause starts or changes, not on every retry.
func TestPersistentFailuresReportOnlyChanges(t *testing.T) {
	f := fakeSource("first")
	m, _ := testImporter(t, f)
	w := m.workers[0]
	reports := map[string][]string{}
	m.deps.OnError = func(_, session string, err error) { reports[session] = append(reports[session], err.Error()) }
	sweep := func(want map[string]int) {
		t.Helper()
		_ = w.sweepChecked(t.Context())
		for session, n := range want {
			if len(reports[session]) != n {
				t.Fatalf("reports for %q = %d %v, want %d", session, len(reports[session]), reports[session], n)
			}
		}
	}
	f.snapshotErr = platforms.ErrBusy
	sweep(map[string]int{"session": 1, "": 1})
	sweep(map[string]int{"session": 1, "": 1})
	sweep(map[string]int{"session": 1, "": 1})

	f.snapshotErr = errors.New("transcript unreadable")
	sweep(map[string]int{"session": 2, "": 1})

	f.snapshotErr = nil
	sweep(map[string]int{"session": 2, "": 1})
	f.mu.Lock()
	f.entries[0].Version = "v2"
	f.mu.Unlock()
	f.snapshotErr = platforms.ErrBusy
	sweep(map[string]int{"session": 3, "": 2})
}
