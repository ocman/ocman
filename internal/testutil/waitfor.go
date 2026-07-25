package testutil

import (
	"testing"
	"time"
)

// waitForPoll is the gap between condition checks. Small enough that a
// fast condition doesn't add measurable test time, large enough not to
// spin a core on a loaded CI runner.
const waitForPoll = 5 * time.Millisecond

// WaitFor polls cond until it returns true or timeout elapses, then
// fails the test with msg. Use it instead of "sleep a bit, then assert":
// a fixed sleep is simultaneously too long on a fast machine and too
// short on a loaded CI runner, which is the classic flake shape.
//
// cond must be safe to call repeatedly from the test goroutine — guard
// any state the subject under test mutates concurrently.
//
// It does not work for asserting the *absence* of an event; keep an
// explicit sleep for that.
func WaitFor(t *testing.T, timeout time.Duration, msg string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", timeout, msg)
		}
		time.Sleep(waitForPoll)
	}
}
