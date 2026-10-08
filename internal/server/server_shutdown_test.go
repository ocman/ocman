package server

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestServerShutdownWaitsForStartedWorkers(t *testing.T) {
	t.Setenv("OCMAN_PLUGIN_DIR", t.TempDir())
	srv := testServer(t)
	entered, release, exited := make(chan struct{}), make(chan struct{}), make(chan struct{})
	original := autoArchiveTickFn
	autoArchiveTickFn = func(*Server) { close(entered); <-release; close(exited) }
	defer func() { autoArchiveTickFn = original }()
	defer func() { close(release); <-exited }()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- srv.StartOnListener(ctx, ln) }()
	<-entered
	// Wait for startup to complete, so only the blocked worker can delay shutdown.
	for {
		resp, err := http.Get("http://" + ln.Addr().String() + "/api/routines")
		if err == nil {
			_ = resp.Body.Close()
			break
		}
		select {
		case <-time.After(time.Millisecond):
		case <-t.Context().Done():
			t.Fatal(err)
		}
	}
	cancel()
	select {
	case err := <-done:
		t.Fatalf("shutdown returned before its worker exited: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	// The deferred release lets the worker finish before the fixture is removed.
	t.Cleanup(func() {
		if err := <-done; err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	})
}
