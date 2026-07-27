package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// countingFlusher records Flush calls so the test can assert the
// flush path is exercised under the same lock as Write.
type countingFlusher struct {
	http.ResponseWriter
	mu      sync.Mutex
	flushes int
}

func (c *countingFlusher) Flush() {
	c.mu.Lock()
	c.flushes++
	c.mu.Unlock()
}

// TestLazyHeaderWriterConcurrentWrites reproduces the SSE
// ResponseWriter race. lw is written by the request goroutine via
// Tee -> ProxyEvents AND by the auto-approve background goroutine via
// the registered Sink; Sink.mu only serialises sinks against each
// other. Run under -race: without the mutex the unsynchronised `wrote`
// bool and the concurrent underlying Write are both reported.
func TestLazyHeaderWriterConcurrentWrites(t *testing.T) {
	rec := httptest.NewRecorder()
	target := &countingFlusher{ResponseWriter: rec}
	lw := &lazyHeaderWriter{ResponseWriter: target}

	const writers = 8
	const perWriter = 200
	frame := []byte("event: ocman.permission.pending\ndata: {}\n\n")

	var wg sync.WaitGroup
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range perWriter {
				if _, err := lw.Write(frame); err != nil {
					t.Errorf("Write: %v", err)
					return
				}
				lw.Flush()
				_ = lw.Wrote()
			}
		}()
	}
	wg.Wait()

	if !lw.Wrote() {
		t.Error("Wrote() = false after writing")
	}
	if got := rec.Body.Len(); got != writers*perWriter*len(frame) {
		t.Errorf("body length = %d, want %d", got, writers*perWriter*len(frame))
	}
	// Frames must not interleave: every chunk between blank-line
	// separators has to be a whole frame.
	if n := bytes.Count(rec.Body.Bytes(), frame); n != writers*perWriter {
		t.Errorf("whole frames = %d, want %d (frames interleaved mid-write)", n, writers*perWriter)
	}
	if strings.Contains(rec.Body.String(), "event: ocman.permission.pending\nevent:") {
		t.Error("detected an interleaved SSE frame")
	}
	if target.flushes != writers*perWriter {
		t.Errorf("flushes = %d, want %d", target.flushes, writers*perWriter)
	}
}

// TestLazyHeaderWriterWroteStartsFalse pins the 503 fast path: no bytes
// written means Wrote() is false so serveSessionEvents can still send a
// real status.
func TestLazyHeaderWriterWroteStartsFalse(t *testing.T) {
	lw := &lazyHeaderWriter{ResponseWriter: httptest.NewRecorder()}
	if lw.Wrote() {
		t.Fatal("Wrote() = true before any write")
	}
	if _, err := lw.Write(nil); err != nil {
		t.Fatalf("Write(nil): %v", err)
	}
	if lw.Wrote() {
		t.Error("Wrote() = true after a zero-length write")
	}
	if _, err := lw.Write([]byte("x")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !lw.Wrote() {
		t.Error("Wrote() = false after a real write")
	}
}
