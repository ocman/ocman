package httpx

import (
	"bytes"
	"testing"
)

func TestSSEFrameWriterRejectsOversizedFrame(t *testing.T) {
	var dst bytes.Buffer
	w := &SSEFrameWriter{Dst: &dst}

	n, err := w.Write(bytes.Repeat([]byte{'x'}, MaxSSEFrameBytes+1))

	if err == nil || n != 0 {
		t.Fatalf("Write = (%d, %v), want (0, error)", n, err)
	}
	if dst.Len() != 0 || len(w.pending) != 0 {
		t.Fatalf("oversized frame was retained or forwarded")
	}
}

func TestSSEFrameWriterForwardsWholeFrames(t *testing.T) {
	var dst bytes.Buffer
	w := &SSEFrameWriter{Dst: &dst}
	_, _ = w.Write([]byte("data: {\"a\""))
	if dst.Len() != 0 {
		t.Fatalf("partial frame forwarded: %q", dst.String())
	}
	_, _ = w.Write([]byte(":1}\n\ndata: x\r\n\r\ndata: tail"))
	if got := dst.String(); got != "data: {\"a\":1}\n\ndata: x\r\n\r\n" {
		t.Fatalf("forwarded %q", got)
	}
}

func TestSSEFrameWriterBoundsEveryFrame(t *testing.T) {
	big := bytes.Repeat([]byte{'x'}, MaxSSEFrameBytes+1)
	for name, input := range map[string][]byte{
		"terminated oversized frame":   append(append([]byte{}, big...), "\n\n"...),
		"oversized tail after a frame": append([]byte("data: ok\n\n"), big...),
	} {
		t.Run(name, func(t *testing.T) {
			w := &SSEFrameWriter{Dst: &bytes.Buffer{}}
			if _, err := w.Write(input); err == nil {
				t.Fatal("oversized frame accepted")
			}
			if len(w.pending) != 0 {
				t.Fatal("oversized frame retained")
			}
		})
	}
}
