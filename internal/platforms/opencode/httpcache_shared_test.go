package opencode

import (
	"context"
	"testing"
	"time"
)

func TestHTTPCacheSharedFetchSurvivesOneCallerCancelling(t *testing.T) {
	c := newHTTPCache(time.Second)
	started := make(chan struct{})
	release := make(chan struct{})
	upstreamCancelled := make(chan struct{}, 1)
	fetch := func(ctx context.Context) ([]byte, bool) {
		close(started)
		select {
		case <-release:
			return []byte("body"), true
		case <-ctx.Done():
			upstreamCancelled <- struct{}{}
			return nil, false
		}
	}
	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	leader := make(chan bool)
	go func() {
		_, ok := c.getOrFetchShared(leaderCtx, "1", "/p", fetch)
		leader <- ok
	}()
	<-started
	joined := make(chan []byte)
	go func() {
		body, _ := c.getOrFetchShared(context.Background(), "1", "/p", fetch)
		joined <- body
	}()
	// Let the second caller join the flight before the leader leaves.
	waitFor(t, 2*time.Second, func() bool {
		c.sharedMu.Lock()
		defer c.sharedMu.Unlock()
		return c.shared["1|/p"] != nil && c.shared["1|/p"].waiters == 2
	})
	cancelLeader()
	if <-leader {
		t.Fatal("cancelled caller reported success")
	}
	close(release)
	if body := <-joined; string(body) != "body" {
		t.Fatalf("joined caller got %q after the leader cancelled", body)
	}
	select {
	case <-upstreamCancelled:
		t.Fatal("shared upstream request was cancelled while a caller still waited")
	default:
	}
}

func TestHTTPCacheSharedFetchCancelledWhenAllCallersLeave(t *testing.T) {
	c := newHTTPCache(time.Second)
	started := make(chan struct{})
	upstreamCancelled := make(chan struct{})
	fetch := func(ctx context.Context) ([]byte, bool) {
		close(started)
		<-ctx.Done()
		close(upstreamCancelled)
		return nil, false
	}
	ctx, cancel := context.WithCancel(context.Background())
	go c.getOrFetchShared(ctx, "1", "/p", fetch)
	<-started
	cancel()
	select {
	case <-upstreamCancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("abandoned upstream request kept running")
	}
}
