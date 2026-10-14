package httpx

import (
	"bytes"
	"fmt"
	"io"
	"sync"
)

// MaxSSEFrameBytes bounds one buffered SSE frame.
const MaxSSEFrameBytes = 4 << 20

// SSEFrameWriter forwards only complete SSE frames to Dst, so other writers
// sharing Dst (synthetic events, keepalives) never land inside a frame.
type SSEFrameWriter struct {
	Dst     io.Writer
	mu      sync.Mutex
	pending []byte
}

func (w *SSEFrameWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = append(w.pending, p...)
	for {
		end := sseFrameEnd(w.pending)
		size := end
		if end < 0 {
			size = len(w.pending)
		}
		if size > MaxSSEFrameBytes {
			w.pending = nil
			return 0, fmt.Errorf("SSE frame exceeds %d bytes", MaxSSEFrameBytes)
		}
		if end < 0 {
			return len(p), nil
		}
		if _, err := w.Dst.Write(w.pending[:end]); err != nil {
			return len(p), err
		}
		w.pending = w.pending[end:]
		if len(w.pending) == 0 {
			w.pending = nil
		}
	}
}

func sseFrameEnd(p []byte) int {
	lf, crlf := bytes.Index(p, []byte("\n\n")), bytes.Index(p, []byte("\n\r\n"))
	if lf >= 0 && (crlf < 0 || lf < crlf) {
		return lf + 2
	}
	if crlf >= 0 {
		return crlf + 3
	}
	return -1
}

// SSEKeepalive is a named event, unlike a comment, so browser clients can
// tell a live stream from a half-open one.
const SSEKeepalive = "event: ocman.keepalive\ndata: {}\n\n"
