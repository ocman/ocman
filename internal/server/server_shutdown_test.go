package server

import (
	"net"
	"net/http"
	"testing"
	"time"
)

// A request that outlives the grace period is cut off: shutdown (and so a
// platform reload) never fails because of it.
func TestShutdownHTTPForcesCloseAfterTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	srv := newHTTPServer(ln.Addr().String(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(entered)
		<-release
	}))
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
}
