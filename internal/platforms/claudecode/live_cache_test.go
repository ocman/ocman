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

// TestLiveCache_TrackToolStartAndEnd verifies the hook-driven
// in-flight-tool tracking: PreToolUse pushes, PostToolUse pops, and
// the live state surfaces the intermediate list.
func TestLiveCache_TrackToolStartAndEnd(t *testing.T) {
	c := newLiveCache(busyTTLForTest)
	now := time.Now()

	c.ApplyAt("s1", liveStateDelta{
		Status:    "busy",
		ToolStart: &toolActivity{SubagentID: "a1", ToolName: "Read", Summary: "/tmp/foo"},
	}, now)
	c.ApplyAt("s1", liveStateDelta{
		Status:    "busy",
		ToolStart: &toolActivity{SubagentID: "a1", ToolName: "Grep", Summary: "TODO"},
	}, now.Add(10*time.Millisecond))

	got := c.GetAt("s1", now.Add(20*time.Millisecond))
	if got == nil {
		t.Fatal("expected state, got nil")
	}
	if len(got.CurrentTools) != 2 {
		t.Fatalf("CurrentTools = %+v, want 2 entries", got.CurrentTools)
	}
	if got.CurrentTools[0].ToolName != "Read" || got.CurrentTools[1].ToolName != "Grep" {
		t.Errorf("unexpected order: %+v", got.CurrentTools)
	}

	// End the Read; only Grep should remain.
	c.ApplyAt("s1", liveStateDelta{
		Status:  "busy",
		ToolEnd: &toolActivity{SubagentID: "a1", ToolName: "Read"},
	}, now.Add(20*time.Millisecond))
	got = c.GetAt("s1", now.Add(30*time.Millisecond))
	if len(got.CurrentTools) != 1 || got.CurrentTools[0].ToolName != "Grep" {
		t.Errorf("after PostToolUse Read: %+v", got.CurrentTools)
	}
}

// TestLiveCache_ToolStartDeduplicates covers the re-invocation case:
// the same (subagent, tool) pair starting twice must not stack.
func TestLiveCache_ToolStartDeduplicates(t *testing.T) {
	c := newLiveCache(busyTTLForTest)
	now := time.Now()
	c.ApplyAt("s1", liveStateDelta{
		ToolStart: &toolActivity{SubagentID: "a1", ToolName: "Read", Summary: "/tmp/foo"},
	}, now)
	c.ApplyAt("s1", liveStateDelta{
		ToolStart: &toolActivity{SubagentID: "a1", ToolName: "Read", Summary: "/tmp/bar"},
	}, now.Add(time.Millisecond))
	got := c.GetAt("s1", now.Add(2*time.Millisecond))
	if len(got.CurrentTools) != 1 {
		t.Fatalf("want 1 tool after dedup, got %+v", got.CurrentTools)
	}
	if got.CurrentTools[0].Summary != "/tmp/bar" {
		t.Errorf("Summary should reflect the latest start, got %q", got.CurrentTools[0].Summary)
	}
}

// TestLiveCache_SubagentEndClearsTools verifies that a SubagentStop
// wipes every tool tracked for that sub-agent without touching
// others.
func TestLiveCache_SubagentEndClearsTools(t *testing.T) {
	c := newLiveCache(busyTTLForTest)
	now := time.Now()
	c.ApplyAt("s1", liveStateDelta{ToolStart: &toolActivity{SubagentID: "a1", ToolName: "Read"}}, now)
	c.ApplyAt("s1", liveStateDelta{ToolStart: &toolActivity{SubagentID: "a2", ToolName: "Bash"}}, now)
	c.ApplyAt("s1", liveStateDelta{SubagentEnd: "a1"}, now.Add(time.Millisecond))

	got := c.GetAt("s1", now.Add(2*time.Millisecond))
	if len(got.CurrentTools) != 1 || got.CurrentTools[0].SubagentID != "a2" {
		t.Errorf("after SubagentStop a1: %+v", got.CurrentTools)
	}
}

// TestLiveCache_TerminalStatusClearsTools ensures a parent-level Stop
// sweeps any leftover tool entries — defensive against dropped
// PostToolUse events.
func TestLiveCache_TerminalStatusClearsTools(t *testing.T) {
	c := newLiveCache(busyTTLForTest)
	now := time.Now()
	c.ApplyAt("s1", liveStateDelta{ToolStart: &toolActivity{ToolName: "Read"}}, now)
	c.ApplyAt("s1", liveStateDelta{Status: "done", ClearPendingPermission: true}, now.Add(time.Millisecond))

	got := c.GetAt("s1", now.Add(2*time.Millisecond))
	if got.Status != "done" {
		t.Errorf("Status = %q, want done", got.Status)
	}
	if len(got.CurrentTools) != 0 {
		t.Errorf("expected no tools after Stop, got %+v", got.CurrentTools)
	}
}

// TestLiveCache_ParentTaskSurvivesStrayEmptySubagentEnd is a direct
// regression for the bug that caused sub-agent tool calls to vanish
// from the main session's live list. In the original implementation a
// SubagentStop with no agent_id produced SubagentEnd="" and the cache's
// sweep loop deleted every entry with matching SubagentID — including
// the parent's own Task entry (which has SubagentID=""). The parser
// now guards against emitting SubagentEnd=""; this test pins the
// cache's own behavior so the parent-level Task entry survives a
// zero-delta regardless of upstream guards.
func TestLiveCache_ParentTaskSurvivesStrayEmptySubagentEnd(t *testing.T) {
	c := newLiveCache(busyTTLForTest)
	now := time.Now()
	// Parent fires Task tool_use (sub-agent id is "" because Task runs
	// at the parent level).
	c.ApplyAt("s1", liveStateDelta{
		Status:    "busy",
		ToolStart: &toolActivity{SubagentID: "", ToolName: "Task", Summary: "explore"},
	}, now)
	// Simulate the pathological case: a zero-delta (hooks_parse guards
	// against this, but the cache must still be safe in isolation).
	c.ApplyAt("s1", liveStateDelta{}, now.Add(time.Millisecond))

	got := c.GetAt("s1", now.Add(2*time.Millisecond))
	if len(got.CurrentTools) != 1 || got.CurrentTools[0].ToolName != "Task" {
		t.Fatalf("parent Task was lost: %+v", got.CurrentTools)
	}
}

// TestLiveCache_BusyTTLDropsTools confirms the stale-busy recovery
// path also discards phantom tool entries, matching the "clean slate
// on revival" contract.
func TestLiveCache_BusyTTLDropsTools(t *testing.T) {
	c := newLiveCache(50 * time.Millisecond)
	start := time.Now()
	c.ApplyAt("s1", liveStateDelta{
		Status:    "busy",
		ToolStart: &toolActivity{ToolName: "Read"},
	}, start)
	got := c.GetAt("s1", start.Add(time.Second))
	if got.Status != "done" {
		t.Errorf("Status after TTL = %q, want done", got.Status)
	}
	if len(got.CurrentTools) != 0 {
		t.Errorf("CurrentTools should be empty after busy TTL, got %+v", got.CurrentTools)
	}
}
