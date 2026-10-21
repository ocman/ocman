package conversationingest

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
)

func bulkHistory(count int) *fakeHistory {
	f := fakeSource("bulk")
	f.entries = nil
	f.snapshots = map[string]*db.HistorySnapshot{}
	for i := range count {
		id := fmt.Sprintf("session-%d", i)
		s := historySnapshot(id)
		s.Session.ID, s.Session.ParentID = id, ""
		s.Messages[0].SessionID, s.Messages[0].ID = id, id+"-message"
		s.Parts[0].SessionID, s.Parts[0].MessageID, s.Parts[0].ID = id, id+"-message", id+"-part"
		f.entries = append(f.entries, db.HistoryEntry{ID: id, Version: "v1"})
		f.snapshots[id] = s
	}
	return f
}

func TestBackfillYieldsBetweenExpensiveSessions(t *testing.T) {
	f := bulkHistory(3)
	f.afterSnapshot = func() { time.Sleep(20 * time.Millisecond) }
	m, _ := testImporter(t, f)
	start := time.Now()
	if err := m.workers[0].sweepChecked(t.Context()); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < 180*time.Millisecond {
		t.Fatalf("bulk import monopolized the worker without yielding: %v for three 20ms sessions", elapsed)
	}
}

func TestBackfillSerializesSources(t *testing.T) {
	a, b := fakeSource("first"), fakeSource("second")
	entered, release := make(chan struct{}, 2), make(chan struct{})
	var active, peak atomic.Int32
	read := func() {
		n := active.Add(1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		entered <- struct{}{}
		<-release
		active.Add(-1)
	}
	a.afterSnapshot, b.afterSnapshot = read, read
	m, _ := testImporter(t, a, b)
	stop := runModule(t, m)
	var once sync.Once
	defer stop()
	defer once.Do(func() { close(release) })
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("initial backfill never started")
	}
	select {
	case <-entered:
		t.Fatalf("background sources import concurrently: peak=%d", peak.Load())
	case <-time.After(50 * time.Millisecond):
	}
	once.Do(func() { close(release) })
	eventually(t, "second source did not resume", func() bool { return len(entered) == 1 })
}
