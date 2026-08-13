package relay

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// seedShareOn creates a share whose id is dated `at`, with one chunk.
func (h *harness) seedShareOn(at time.Time) createResponse {
	h.t.Helper()
	saved := h.now
	h.now = at
	defer func() { h.now = saved }()

	created := h.create()
	h.appendChunk(created, 0, []byte("payload"))
	return created
}

func TestSweep_DeletesExpiredShares(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.TTL = 7 * 24 * time.Hour })
	old := h.seedShareOn(h.now.AddDate(0, 0, -10))
	fresh := h.seedShareOn(h.now.AddDate(0, 0, -2))

	if err := h.srv.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}

	if rec := h.do(http.MethodGet, "/s/"+old.ID, nil, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("expired share: status %d, want 404", rec.Code)
	}
	if rec := h.do(http.MethodGet, "/s/"+fresh.ID, nil, ""); rec.Code != http.StatusOK {
		t.Fatalf("live share: status %d, want 200", rec.Code)
	}
}

// TestSweep_KeepsShareUntilTTLElapses pins the boundary: a share exactly
// at the TTL is still live, so the sweeper never deletes early.
func TestSweep_KeepsShareUntilTTLElapses(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.TTL = 7 * 24 * time.Hour })
	atBoundary := h.seedShareOn(h.now.AddDate(0, 0, -7))

	if err := h.srv.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if rec := h.do(http.MethodGet, "/s/"+atBoundary.ID, nil, ""); rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: a share at exactly the TTL must survive", rec.Code)
	}
}

func TestSweep_RemovesEveryObjectOfAnExpiredShare(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.TTL = 24 * time.Hour })
	old := h.seedShareOn(h.now.AddDate(0, 0, -5))
	h.appendChunk(old, 1, []byte("more"))

	if err := h.srv.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	objs, err := h.store.List(context.Background(), "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(objs) != 0 {
		t.Fatalf("sweep left %d objects: %v", len(objs), objs)
	}
}

// TestSweep_IsIdempotent proves a repeated sweep over already-empty days
// is harmless, which is what lets the sweeper delete computed prefixes
// without listing the store first.
func TestSweep_IsIdempotent(t *testing.T) {
	h := newHarness(t, nil)
	fresh := h.create()
	for range 3 {
		if err := h.srv.Sweep(context.Background()); err != nil {
			t.Fatalf("Sweep: %v", err)
		}
	}
	if rec := h.do(http.MethodGet, "/s/"+fresh.ID, nil, ""); rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
}

func TestSweep_LeavesSharesOlderThanTheWindow(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.TTL = 24 * time.Hour })
	ancient := h.seedShareOn(h.now.AddDate(0, 0, -(sweepWindowDays + 30)))

	if err := h.srv.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	// Documents the known ceiling rather than pretending it is covered:
	// a relay offline for longer than the window needs a wider window
	// or a storage lifecycle rule.
	if rec := h.do(http.MethodGet, "/s/"+ancient.ID, nil, ""); rec.Code != http.StatusOK {
		t.Fatalf("status %d: shares beyond the sweep window are expected to survive", rec.Code)
	}
}

func TestRun_SweepsImmediatelyThenStops(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.TTL = 24 * time.Hour })
	old := h.seedShareOn(h.now.AddDate(0, 0, -5))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		h.srv.Run(ctx, func(error) {})
		close(done)
	}()

	deadline := time.After(2 * time.Second)
	for {
		if rec := h.do(http.MethodGet, "/s/"+old.ID, nil, ""); rec.Code == http.StatusNotFound {
			break
		}
		select {
		case <-deadline:
			t.Fatal("Run did not sweep on start")
		case <-time.After(5 * time.Millisecond):
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop when its context was cancelled")
	}
}
