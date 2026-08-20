package autoapprove

import (
	"io"
	"strings"
	"testing"
)

// #462: the tee buffer must stay bounded and parsing must not re-scan
// already-consumed bytes. A stream that never terminates an event (no
// blank line) previously grew t.buf without limit and re-split the
// whole buffer on every Write.

// teeBufBytes reports the tee's internal pending memory (partial line
// plus accumulated data lines).
func teeBufBytes(t *Tee) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	total := len(t.buf)
	for _, l := range t.dataLines {
		total += len(l)
	}
	return total
}

func TestTeeBufferBoundedWithoutEventTerminator(t *testing.T) {
	tee := &Tee{W: io.Discard}

	// One giant line, no '\n' at all: 8 MB in 64 KB chunks.
	chunk := []byte(strings.Repeat("x", 64<<10))
	for i := 0; i < 128; i++ {
		if _, err := tee.Write(chunk); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if got := teeBufBytes(tee); got > teeMaxPendingBytes {
		t.Fatalf("pending bytes = %d, want <= %d (cap)", got, teeMaxPendingBytes)
	}

	// Data lines with terminators but never a blank line: the pending
	// event must be capped too.
	line := []byte("data: " + strings.Repeat("y", 64<<10) + "\n")
	for i := 0; i < 128; i++ {
		if _, err := tee.Write(line); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if got := teeBufBytes(tee); got > teeMaxPendingBytes {
		t.Fatalf("pending bytes after data lines = %d, want <= %d (cap)", got, teeMaxPendingBytes)
	}
}

func TestTeeResyncsAfterOverflow(t *testing.T) {
	var idle []string
	tee := &Tee{
		W:             io.Discard,
		OnSessionIdle: func(sessionID string) { idle = append(idle, sessionID) },
	}

	// Overflow with a giant unterminated line.
	if _, err := tee.Write([]byte(strings.Repeat("x", teeMaxPendingBytes+1))); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Finish the corrupt event: rest of the line, then the terminator.
	// Everything up to the blank line is discarded (resync).
	if _, err := tee.Write([]byte("tail-of-giant-line\n\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	// A well-formed event after the resync point must dispatch normally.
	if _, err := tee.Write([]byte("data: {\"type\":\"session.idle\",\"properties\":{\"sessionID\":\"s1\"}}\n\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	if len(idle) != 1 || idle[0] != "s1" {
		t.Fatalf("idle = %v, want [s1] (parser must resync after overflow)", idle)
	}
}

// Incremental parsing: events split across many small writes still
// dispatch exactly once, and bytes are consumed as lines complete.
func TestTeeIncrementalParseAcrossWrites(t *testing.T) {
	var idle []string
	tee := &Tee{
		W:             io.Discard,
		OnSessionIdle: func(sessionID string) { idle = append(idle, sessionID) },
	}
	payload := "data: {\"type\":\"session.idle\",\"properties\":{\"sessionID\":\"s2\"}}\n\n"
	for _, b := range []byte(payload) {
		if _, err := tee.Write([]byte{b}); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if len(idle) != 1 || idle[0] != "s2" {
		t.Fatalf("idle = %v, want [s2]", idle)
	}
	if got := teeBufBytes(tee); got != 0 {
		t.Fatalf("pending bytes = %d, want 0 after a completed event", got)
	}
}
