package conversationingest

import (
	"errors"
	"time"
)

var errDeferred = errors.New("history snapshot is still changing; retry required")

func (w *sourceWorker) requireComplete(err error) error {
	if err == nil && w.last.Counters["deferred"] > 0 {
		return errDeferred
	}
	return err
}

// Failure retries are bounded, independent of new source events, and never
// turn healthy event-fed sources back into periodic content polling.
type retryBackoff struct {
	next  time.Time
	delay time.Duration
}

func (r *retryBackoff) due() bool { return !time.Now().Before(r.next) }
func (r *retryBackoff) fail() {
	if r.delay == 0 {
		r.delay = 100 * time.Millisecond
	} else {
		r.delay = min(2*r.delay, 30*time.Second)
	}
	r.next = time.Now().Add(r.delay)
}
