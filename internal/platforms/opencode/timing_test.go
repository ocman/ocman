package opencode

import (
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

// TestObserveDuration_LogsAtInfoWhenAboveThreshold verifies that durations
// at or above the threshold log at INFO so they show up in default
// production logs without DEBUG noise.
func TestObserveDuration_LogsAtInfoWhenAboveThreshold(t *testing.T) {
	hook := logtest.NewGlobal()
	defer hook.Reset()
	prev := log.GetLevel()
	log.SetLevel(log.DebugLevel)
	defer log.SetLevel(prev)

	observeDuration("test_op", 250*time.Millisecond, log.Fields{"sessionID": "abc"})

	if len(hook.AllEntries()) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(hook.AllEntries()))
	}
	e := hook.AllEntries()[0]
	if e.Level != log.InfoLevel {
		t.Errorf("level: got %v, want %v", e.Level, log.InfoLevel)
	}
	if got, _ := e.Data["op"].(string); got != "test_op" {
		t.Errorf("op: got %v, want test_op", e.Data["op"])
	}
	if got, _ := e.Data["sessionID"].(string); got != "abc" {
		t.Errorf("sessionID propagated: got %v, want abc", e.Data["sessionID"])
	}
	if got, _ := e.Data["duration_ms"].(int64); got != 250 {
		t.Errorf("duration_ms: got %v, want 250", e.Data["duration_ms"])
	}
}

// TestObserveDuration_LogsAtDebugBelowThreshold ensures sub-threshold
// observations don't pollute the operator log. They're still recorded
// at DEBUG so a debug-level log captures them when needed.
func TestObserveDuration_LogsAtDebugBelowThreshold(t *testing.T) {
	hook := logtest.NewGlobal()
	defer hook.Reset()
	prev := log.GetLevel()
	log.SetLevel(log.DebugLevel)
	defer log.SetLevel(prev)

	observeDuration("test_op", 50*time.Millisecond, nil)

	if len(hook.AllEntries()) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(hook.AllEntries()))
	}
	if e := hook.AllEntries()[0]; e.Level != log.DebugLevel {
		t.Errorf("level: got %v, want %v", e.Level, log.DebugLevel)
	}
}

// TestTimeIt_RoundTripCapturesElapsed exercises the timeIt closure
// shape callers actually use:
//
//	defer timeIt("op", fields)()
//
// The deferred call must record at least the time we slept for.
func TestTimeIt_RoundTripCapturesElapsed(t *testing.T) {
	hook := logtest.NewGlobal()
	defer hook.Reset()

	func() {
		defer timeIt("test_sleep", log.Fields{"k": "v"})()
		time.Sleep(slowOpThreshold + 20*time.Millisecond)
	}()

	if len(hook.AllEntries()) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(hook.AllEntries()))
	}
	e := hook.AllEntries()[0]
	if e.Level != log.InfoLevel {
		t.Errorf("level: got %v, want INFO", e.Level)
	}
	dur, _ := e.Data["duration_ms"].(int64)
	if dur < slowOpThreshold.Milliseconds() {
		t.Errorf("duration_ms: got %d, want >= %d", dur, slowOpThreshold.Milliseconds())
	}
	if got, _ := e.Data["k"].(string); got != "v" {
		t.Errorf("custom field propagated: got %v, want v", e.Data["k"])
	}
}
