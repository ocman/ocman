package platforms

import (
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type slowReader struct{ gap time.Duration }

func (r slowReader) Read(p []byte) (int, error) {
	time.Sleep(r.gap)
	p[0] = 'x'
	return 1, nil
}

func TestIdleWatchdogFiresOnSilence(t *testing.T) {
	fired := make(chan struct{})
	d := NewIdleWatchdog(10*time.Millisecond, func() { close(fired) })
	defer d.Stop()
	select {
	case <-fired:
	case <-time.After(5 * time.Second):
		t.Fatal("watchdog did not fire")
	}
	if !d.Expired() {
		t.Fatal("Expired() = false after firing")
	}
	d.Touch() // must not rearm (and fire twice) after expiry
	time.Sleep(30 * time.Millisecond)
}

func TestIdleWatchdogReadsKeepItArmed(t *testing.T) {
	var fired atomic.Bool
	d := NewIdleWatchdog(100*time.Millisecond, func() { fired.Store(true) })
	defer d.Stop()
	r := d.Reader(slowReader{gap: 10 * time.Millisecond})
	buf := make([]byte, 1)
	for range 30 { // 300ms of reads, 3x the timeout
		if _, err := r.Read(buf); err != nil {
			t.Fatal(err)
		}
	}
	if fired.Load() || d.Expired() {
		t.Fatal("watchdog fired while bytes were arriving")
	}
}

func TestIdleWatchdogStopAndEmptyReads(t *testing.T) {
	var fired atomic.Bool
	d := NewIdleWatchdog(20*time.Millisecond, func() { fired.Store(true) })
	if _, err := io.ReadAll(d.Reader(strings.NewReader(""))); err != nil {
		t.Fatal(err)
	}
	d.Stop()
	time.Sleep(60 * time.Millisecond)
	if fired.Load() {
		t.Fatal("stopped watchdog fired")
	}
}
