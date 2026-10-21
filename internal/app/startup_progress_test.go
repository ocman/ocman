package app

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

// A deliberately blocked composition models a long database migration. The
// listener must answer progress requests before that work finishes.
func TestStartupProgressServesBeforeComposition(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- startBackend(ctx, ln, func(ctx context.Context, _ net.Listener, _ func()) error {
			close(started)
			<-ctx.Done()
			return nil
		})
	}()
	t.Cleanup(func() { cancel(); <-done })
	<-started
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get("http://" + ln.Addr().String() + "/api/startup")
	if err != nil {
		t.Fatalf("startup progress unavailable while migration runs: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("startup status = %d, want 200", resp.StatusCode)
	}
}
