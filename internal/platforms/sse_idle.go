package platforms

import (
	"io"
	"sync/atomic"
	"time"
)

// SSEIdleTimeout bounds how long an upstream SSE stream may go without
// bytes. OpenCode sends a server.heartbeat on /event and /global/event
// every 10 seconds, so a healthy quiet stream never reaches it; it catches
// a connection that stays open but stopped producing (half-open TCP, a
// wedged instance) faster than OS keepalive would.
const SSEIdleTimeout = 60 * time.Second

// IdleWatchdog calls onIdle once no bytes have been read through Reader
// for its timeout. Stop it when the stream ends.
type IdleWatchdog struct {
	timer   *time.Timer
	timeout time.Duration
	expired atomic.Bool
}

// NewIdleWatchdog arms the watchdog immediately, so it also bounds the wait
// for the first bytes (response headers). onIdle must unblock the reader,
// e.g. by closing the body or cancelling the request context.
func NewIdleWatchdog(timeout time.Duration, onIdle func()) *IdleWatchdog {
	d := &IdleWatchdog{timeout: timeout}
	d.timer = time.AfterFunc(timeout, func() {
		d.expired.Store(true)
		onIdle()
	})
	return d
}

// Reader wraps r so every read that returns bytes rearms the watchdog.
func (d *IdleWatchdog) Reader(r io.Reader) io.Reader { return idleReader{r, d} }

// Expired reports whether the timeout fired.
func (d *IdleWatchdog) Expired() bool { return d.expired.Load() }

// Stop disarms the watchdog; Touch rearms it.
func (d *IdleWatchdog) Stop() { d.timer.Stop() }

// Touch restarts the timeout unless it already fired.
func (d *IdleWatchdog) Touch() {
	if !d.Expired() {
		d.timer.Reset(d.timeout)
	}
}

type idleReader struct {
	r io.Reader
	d *IdleWatchdog
}

func (r idleReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 {
		r.d.Touch()
	}
	return n, err
}
