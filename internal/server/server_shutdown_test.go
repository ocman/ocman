package server

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// A request that outlives the grace period loses its connection, but a
// mutation it detached from cancellation still finishes its durable write
// before shutdown returns: teardown never runs under it, and the stuck
// request never turns a reload into a fatal exit.
func TestShutdownJoinsDetachedHandlerAfterForcedClose(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var fence requestFence
	entered := make(chan struct{})
	var written atomic.Bool
	srv := newHTTPServer(ln.Addr().String(), fence.track(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(entered)
		detached := context.WithoutCancel(r.Context()) // like a confirmed Factory action
		<-r.Context().Done()
		time.Sleep(200 * time.Millisecond) // the receipt outlives the connection
		_ = detached
		written.Store(true)
	})))
	go func() { _ = srv.Serve(ln) }()
	requested := make(chan error, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String())
		if err == nil {
			_ = resp.Body.Close()
		}
		requested <- err
	}()
	<-entered
	if err := shutdownHTTP(srv, 50*time.Millisecond); err != nil {
		t.Fatalf("shutdown = %v, want a forced close", err)
	}
	select {
	case err := <-requested:
		if err == nil {
			t.Fatal("the stuck request completed instead of being cut off")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the stuck connection stayed open")
	}
	fence.close()
	if !written.Load() {
		t.Fatal("shutdown finished before the detached write settled")
	}
}

// Once closed, the fence admits nothing.
func TestRequestFenceRefusesAfterClose(t *testing.T) {
	var fence requestFence
	fence.close()
	ran := false
	rec := httptest.NewRecorder()
	fence.track(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { ran = true })).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if ran || rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("admitted after close: ran=%v code=%d", ran, rec.Code)
	}
}
