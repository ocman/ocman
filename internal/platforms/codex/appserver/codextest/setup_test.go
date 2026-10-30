package codextest

import (
	"io"
	"sync/atomic"
	"testing"
)

type setupReader struct {
	configured *atomic.Bool
	t          *testing.T
}

func (r setupReader) Read([]byte) (int, error) {
	if !r.configured.Load() {
		r.t.Error("read requests before handlers were registered")
	}
	return 0, io.EOF
}

func TestServeRegistersHandlersBeforeReading(t *testing.T) {
	var configured atomic.Bool
	s := Serve(setupReader{configured: &configured, t: t}, io.Discard, func(*Server) { configured.Store(true) })
	<-s.Done()
	if !configured.Load() {
		t.Fatal("setup callback was not called")
	}
}
