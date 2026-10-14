package claudeprovider

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/claudeprovider/claudetest"
	"github.com/NoUseFreak/ocman/internal/db"
)

// Helper calls are bounded machine-wide by their own slots: never more than
// HelperProcs at once, and never blocked by pooled children (a judge must
// not wait behind sessions that wait on its verdict).
func TestHelperSlotsAreBoundedAndSeparate(t *testing.T) {
	dir := t.TempDir()
	for range DefaultMaxProcs { // every session slot is taken
		release, ok, err := trySlot(dir, "slot", DefaultMaxProcs)
		if err != nil || !ok {
			t.Fatalf("session slot: %v %v", ok, err)
		}
		t.Cleanup(release)
	}
	var held []func()
	for range HelperProcs {
		release, err := HelperSlot(t.Context(), dir)
		if err != nil {
			t.Fatalf("helper behind busy sessions: %v", err)
		}
		held = append(held, release)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	if _, err := HelperSlot(ctx, dir); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("helper beyond the budget = %v, want to wait", err)
	}
	held[0]()
	release, err := HelperSlot(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	release()
	held[1]()
}

// A slot store that cannot be used is an error, not contention: helpers fail
// at once and an accepted turn fails visibly instead of staying busy.
func TestBrokenSlotStoreFailsVisibly(t *testing.T) {
	broken := filepath.Join(t.TempDir(), "slots")
	if err := os.WriteFile(broken, nil, 0o600); err != nil { // a file where the directory belongs
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := HelperSlot(ctx, broken); err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("helper slot on a broken store = %v, want an I/O error", err)
	}
	h := startLive(t, claudetest.Fixture("plain.jsonl"), func(o *Options) { o.SlotDir = broken })
	id := h.create(t)
	h.send(t, id, "hi")
	waitFor(t, "turn settled", func() bool { _, _, idles := h.events.get(id); return idles == 1 })
	if s, _, _ := h.events.get(id); s != db.StatusError || !h.visible(id, "claude process slots") {
		t.Fatalf("status = %s, want a visible slot error: %+v", s, h.live.snapshot(id))
	}
	if h.procs() != 0 {
		t.Fatal("a child launched without a slot")
	}
}

// A turn waiting for a machine-wide slot ends when the provider stops
// instead of polling forever.
func TestSlotWaitEndsWhenLiveStops(t *testing.T) {
	dir := t.TempDir()
	for range DefaultMaxProcs {
		release, ok, err := trySlot(dir, "slot", DefaultMaxProcs)
		if err != nil || !ok {
			t.Fatalf("session slot: %v %v", ok, err)
		}
		t.Cleanup(release)
	}
	h := startLive(t, claudetest.Fixture("plain.jsonl"), func(o *Options) { o.SlotDir = dir })
	id := h.create(t)
	h.send(t, id, "hi")
	h.live.mu.Lock()
	tail := h.live.tails[id]
	h.live.mu.Unlock()
	time.Sleep(3 * slotPoll) // the turn is waiting for a slot
	h.live.stopAll()
	select {
	case <-tail:
	case <-time.After(2 * time.Second):
		t.Fatal("the slot wait ignored the provider stopping")
	}
}
