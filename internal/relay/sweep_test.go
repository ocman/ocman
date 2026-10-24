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

// TestSweep_ExpiresSharesPastTheTTL seeds shares of several ages on one
// relay and sweeps repeatedly: a repeated sweep over already-empty days is
// harmless, which is what lets the sweeper delete computed prefixes
// without listing the store first.
func TestSweep_ExpiresSharesPastTheTTL(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.TTL = 7 * 24 * time.Hour })
	// Seeded oldest first so the create rate limiter's clock only moves
	// forward.
	ancient := h.seedShareOn(h.now.AddDate(0, 0, -(sweepWindowDays + 30)))
	expired := h.seedShareOn(h.now.AddDate(0, 0, -10))
	h.appendChunk(expired, 1, []byte("more"))
	atTTL := h.seedShareOn(h.now.AddDate(0, 0, -7))
	live := h.seedShareOn(h.now.AddDate(0, 0, -2))
	shares := []struct {
		name string
		id   string
		want int
	}{
		{"expired share", expired.ID, http.StatusNotFound},
		{"live share", live.ID, http.StatusOK},
		// The boundary: a share exactly at the TTL is still live, so the
		// sweeper never deletes early.
		{"share at exactly the TTL", atTTL.ID, http.StatusOK},
		{"share created now", h.create().ID, http.StatusOK},
		// Documents the known ceiling rather than pretending it is
		// covered: a relay offline for longer than the window needs a
		// wider window or a storage lifecycle rule.
		{"share beyond the sweep window", ancient.ID, http.StatusOK},
	}

	for range 3 {
		if err := h.srv.Sweep(context.Background()); err != nil {
			t.Fatalf("Sweep: %v", err)
		}
	}

	for _, s := range shares {
		if rec := h.do(http.MethodGet, "/s/"+s.id, nil, ""); rec.Code != s.want {
			t.Errorf("%s: status %d, want %d", s.name, rec.Code, s.want)
		}
	}
	prefix, _ := prefixFor(expired.ID)
	objs, err := h.store.List(context.Background(), prefix)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(objs) != 0 {
		t.Fatalf("sweep left %d objects of the expired share: %v", len(objs), objs)
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
