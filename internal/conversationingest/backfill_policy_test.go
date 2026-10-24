package conversationingest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/statev2"
	"github.com/NoUseFreak/ocman/internal/tasksvc"
)

func TestBackfillBudgetTracksActualWork(t *testing.T) {
	for _, tc := range []struct {
		name    string
		percent int
		work    time.Duration
		rest    time.Duration
	}{
		{"default", 20, 40 * time.Millisecond, 160 * time.Millisecond},
		{"minimum pause", 20, time.Microsecond, 10 * time.Millisecond},
		{"unpaced", 100, time.Second, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := newBackfillLimiter(tc.percent)
			now := time.Unix(100, 0)
			l.now = func() time.Time { return now }
			release, err := l.acquire(t.Context(), nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			now = now.Add(tc.work)
			release()
			if got := l.next.Sub(now); got != tc.rest || len(l.slot) != 0 {
				t.Fatalf("rest=%v want=%v slot=%d", got, tc.rest, len(l.slot))
			}
		})
	}
}

func TestBackfillBudgetDefaults(t *testing.T) {
	for _, percent := range []int{-1, 0, 101, 35, 100} {
		m := NewModule(Deps{BackfillDutyPercent: percent})
		want := percent
		if want <= 0 || want > 100 {
			want = 20
		}
		if m.backfill.percent != want {
			t.Fatalf("configured %d: got %d want %d", percent, m.backfill.percent, want)
		}
	}
}

func TestBackfillCancellationReleasesBudget(t *testing.T) {
	for _, mode := range []string{"slot", "cooldown", "already cancelled"} {
		t.Run(mode, func(t *testing.T) {
			l := newBackfillLimiter(20)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch mode {
			case "slot":
				l.slot <- struct{}{}
				defer func() { <-l.slot }()
			case "cooldown":
				l.next = time.Now().Add(time.Hour)
			default:
				cancel()
			}
			done := make(chan error, 1)
			go func() { _, err := l.acquire(ctx, nil, nil); done <- err }()
			if mode == "cooldown" {
				eventually(t, "budget was not acquired", func() bool { return len(l.slot) == 1 })
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("shutdown waited for the backfill cooldown")
			}
			if mode != "slot" && len(l.slot) != 0 {
				t.Fatal("cancelled backfill retained the shared budget")
			}
		})
	}
}

func TestBackfillServicesRequestsWhileAnotherSourceOwnsBudget(t *testing.T) {
	l := newBackfillLimiter(20)
	l.slot <- struct{}{}
	defer func() { <-l.slot }()
	wake, attended := make(chan struct{}, 1), make(chan struct{})
	wake <- struct{}{}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := l.acquire(ctx, wake, func() { close(attended) })
		done <- err
	}()
	select {
	case <-attended:
	case <-time.After(time.Second):
		t.Fatal("foreground work waited behind another source's background slot")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestRequestedConversationBypassesBackfillCooldown(t *testing.T) {
	f := bulkHistory(3)
	m, store := testImporter(t, f)
	w := m.workers[0]
	if err := w.ensureInstance(t.Context()); err != nil {
		t.Fatal(err)
	}
	m.backfill.next = time.Now().Add(time.Hour)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.sweepChecked(ctx) }()
	eventually(t, "backfill did not enter cooldown", func() bool { return len(m.backfill.slot) == 1 })
	readCtx, readCancel := context.WithTimeout(t.Context(), time.Second)
	defer readCancel()
	if !m.AwaitStored(readCtx, "bulk", "session-2") {
		t.Fatal("an opened conversation waited behind the historical import cooldown")
	}
	key := statev2.Key{InstanceID: w.instance.ID, SessionID: "session-2"}
	if _, err := store.ReadConversation(t.Context(), key, 10, 0); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	f.mu.Lock()
	reads := f.reads
	f.mu.Unlock()
	if reads != 1 {
		t.Fatalf("foreground read released the whole backfill: %d reads", reads)
	}
}

func TestBackfillCancellationKeepsCommittedProgress(t *testing.T) {
	f := bulkHistory(3)
	m, store := testImporter(t, f)
	w := m.workers[0]
	if err := w.ensureInstance(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err := w.execute(ctx, pass{background: true, reads: f.entries}, func(p tasksvc.Progress) error {
		if p.Completed == 1 {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if cursor, err := store.Checkpoint(t.Context(), w.instance.ID, snapshotStream("session-0")); err != nil || cursor == "" {
		t.Fatalf("committed snapshot lost its checkpoint: %q %v", cursor, err)
	}
	restarted := NewModule(m.deps).workers[0]
	p, err := restarted.prepare(t.Context(), nil)
	if err != nil || len(p.reads) != 2 || p.skipped != 1 {
		t.Fatalf("restart did not resume remaining work: %+v %v", p, err)
	}
}

func TestPriorityCompletionKeepsNewerRequests(t *testing.T) {
	m, _ := testImporter(t, fakeSource("source"))
	w := m.workers[0]
	key := [2]string{"source", "session"}
	m.receiptMu.Lock()
	m.readRequests = map[[2]string]uint64{key: 2}
	m.readValidated = map[[2]string]uint64{key: 1}
	m.receiptMu.Unlock()
	w.priority = map[string]bool{"session": true}
	w.completePriority("session")
	if !w.priority["session"] {
		t.Fatal("an older validation removed a newer pending read")
	}
	m.publishReceipt("source", "session", 2)
	w.completePriority("session")
	if len(w.priority) != 0 {
		t.Fatal("a completed request remained in the priority queue")
	}
}

func TestPanickingSourceDoesNotLeakBackfillSlot(t *testing.T) {
	f := bulkHistory(1)
	f.afterSnapshot = func() { panic("source failed") }
	m, _ := testImporter(t, f)
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("fixture did not panic")
			}
		}()
		_ = m.workers[0].sweepChecked(t.Context())
	}()
	if len(m.backfill.slot) != 0 {
		t.Fatal("a failed producer retained the shared background slot")
	}
}

func TestPanickingPriorityReadDoesNotLeakCooldownSlot(t *testing.T) {
	l := newBackfillLimiter(20)
	l.next = time.Now().Add(time.Hour)
	wake := make(chan struct{}, 1)
	wake <- struct{}{}
	// Acquire directly so the wake is consumed during the cooldown, not the
	// slot-admission select.
	l.slot <- struct{}{}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("fixture did not panic")
			}
		}()
		_, _ = l.cooldown(t.Context(), wake, func() { panic("priority read failed") })
	}()
	if len(l.slot) != 0 {
		t.Fatal("a failed priority producer retained the background cooldown slot")
	}
}

type priorityFeed struct {
	*fakeFeed
	order  []string
	onRead func(string)
}

func (f *priorityFeed) HistorySnapshot(ctx context.Context, id string) (*db.HistorySnapshot, error) {
	f.order = append(f.order, id)
	if f.onRead != nil {
		f.onRead(id)
	}
	return f.fakeHistory.HistorySnapshot(ctx, id)
}

func TestPriorityReadCannotBeOverwrittenByOlderBackfillSequence(t *testing.T) {
	base := newFakeFeed()
	base.fakeHistory = bulkHistory(3)
	base.sequences = map[string]int64{"session-0": 3, "session-1": 3, "session-2": 3}
	f := &priorityFeed{fakeFeed: base}
	m, store := testImporter(t, base.fakeHistory)
	w := m.workers[0]
	w.source.Reader, w.feed = f, f
	if err := w.ensureInstance(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.onRead = func(id string) {
		if id == "session-0" {
			base.mu.Lock()
			base.sequences["session-2"] = 7
			base.mu.Unlock()
			m.requestRead("bulk", "session-2")
		}
	}
	if err := w.catchUpFeed(t.Context(), map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	if len(f.order) != 3 || f.order[0] != "session-0" || f.order[1] != "session-2" || f.order[2] != "session-1" {
		t.Fatalf("backfill did not prioritize and deduplicate the requested session: %v", f.order)
	}
	got, err := store.Checkpoint(t.Context(), w.instance.ID, sequencePrefix+"session-2")
	if err != nil || got != "seq:7" {
		t.Fatalf("old catch-up inventory overwrote newer foreground progress: %q %v", got, err)
	}
}
