package claudecode

import (
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// TestLiveCache_ApplyAndGet verifies the basic round-trip: apply a
// delta derived from a hook event, then read it back through Get.
func TestLiveCache_ApplyAndGet(t *testing.T) {
	c := newLiveCache(busyTTLForTest)
	now := time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)

	c.ApplyAt("s1", liveStateDelta{Status: "busy"}, now)
	got := c.GetAt("s1", now)
	if got == nil {
		t.Fatal("expected state for s1, got nil")
	}
	if got.Status != "busy" {
		t.Errorf("Status = %q, want busy", got.Status)
	}
	if !got.LastEventAt.Equal(now) {
		t.Errorf("LastEventAt = %v, want %v", got.LastEventAt, now)
	}
}

// TestLiveCache_GetUnknownSession returns nil rather than a zero
// LiveState so callers can distinguish "no data" from "explicit
// empty state".
func TestLiveCache_GetUnknownSession(t *testing.T) {
	c := newLiveCache(busyTTLForTest)
	if got := c.GetAt("unknown", time.Now()); got != nil {
		t.Errorf("expected nil for unknown session, got %+v", got)
	}
}

// TestLiveCache_LaterEventsWin covers the transition table: UserPromptSubmit
// -> busy, then Stop -> done. The final state must reflect the latest
// delta, not an accumulation.
func TestLiveCache_LaterEventsWin(t *testing.T) {
	c := newLiveCache(busyTTLForTest)
	now := time.Now()
	c.ApplyAt("s1", liveStateDelta{Status: "busy"}, now)
	c.ApplyAt("s1", liveStateDelta{Status: "done"}, now.Add(time.Second))

	got := c.GetAt("s1", now.Add(2*time.Second))
	if got == nil || got.Status != "done" {
		t.Fatalf("Status = %+v, want done", got)
	}
}

// TestLiveCache_PendingPermissionClears verifies the distinct
// ClearPendingPermission signal: a Notification sets the flag, a
// subsequent UserPromptSubmit (which carries ClearPendingPermission=true)
// must wipe it even though its own PendingPermission is false.
func TestLiveCache_PendingPermissionClears(t *testing.T) {
	c := newLiveCache(busyTTLForTest)
	now := time.Now()

	c.ApplyAt("s1", liveStateDelta{PendingPermission: true}, now)
	got := c.GetAt("s1", now)
	if got == nil || !got.PendingPermission {
		t.Fatalf("after Notification, want PendingPermission=true, got %+v", got)
	}

	c.ApplyAt("s1", liveStateDelta{Status: "busy", ClearPendingPermission: true}, now.Add(time.Second))
	got = c.GetAt("s1", now.Add(time.Second))
	if got == nil || got.PendingPermission {
		t.Fatalf("after UserPromptSubmit, want PendingPermission=false, got %+v", got)
	}
	if got.Status != "busy" {
		t.Errorf("Status = %q, want busy", got.Status)
	}
}

// TestLiveCache_BusyGoesStaleAfterTTL models the safety net: if Claude
// Code crashes mid-turn, the "busy" flag shouldn't stick forever.
// Beyond the TTL, GetAt reports status="done" rather than a stuck "busy".
func TestLiveCache_BusyGoesStaleAfterTTL(t *testing.T) {
	c := newLiveCache(100 * time.Millisecond)
	now := time.Now()

	c.ApplyAt("s1", liveStateDelta{Status: "busy"}, now)

	// Still within TTL — busy is authoritative.
	if got := c.GetAt("s1", now.Add(50*time.Millisecond)); got == nil || got.Status != "busy" {
		t.Errorf("within TTL: expected busy, got %+v", got)
	}

	// Past TTL — cache reports done.
	got := c.GetAt("s1", now.Add(200*time.Millisecond))
	if got == nil || got.Status != "done" {
		t.Errorf("past TTL: expected stale busy -> done, got %+v", got)
	}
}

// TestLiveCache_DoneDoesNotExpire a terminal "done" state should not
// decay back to "done" (no-op) but also must not become something
// weirder. The TTL logic applies only to busy.
func TestLiveCache_DoneDoesNotExpire(t *testing.T) {
	c := newLiveCache(10 * time.Millisecond)
	now := time.Now()
	c.ApplyAt("s1", liveStateDelta{Status: "done"}, now)

	got := c.GetAt("s1", now.Add(time.Hour))
	if got == nil || got.Status != "done" {
		t.Errorf("done should stay done forever, got %+v", got)
	}
}

// TestLiveCache_Concurrent exercises the mutex: many goroutines
// applying deltas to the same session must not panic or data-race.
// Race is detected under -race; the test also asserts the cache is
// in a consistent state after the fan-out.
func TestLiveCache_Concurrent(t *testing.T) {
	c := newLiveCache(busyTTLForTest)
	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c.ApplyAt("s1", liveStateDelta{Status: "busy"}, start.Add(time.Duration(i)*time.Millisecond))
		}(i)
	}
	wg.Wait()
	if got := c.GetAt("s1", start.Add(time.Second)); got == nil || got.Status != "busy" {
		t.Fatalf("after fan-out, want busy, got %+v", got)
	}
}

// Ensure the cache satisfies the platforms.LiveState contract —
// LiveStatus returns a pointer that the caller can safely consume.
var _ *platforms.LiveState = (&liveCache{}).GetAt("", time.Now())

// busyTTLForTest is long enough that TTL never expires in unit tests
// that don't explicitly exercise it.
const busyTTLForTest = time.Hour
