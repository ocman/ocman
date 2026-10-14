package opencode

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestInvalidationFencesInFlightPortScan(t *testing.T) {
	resetPortCache()
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	var calls atomic.Int32
	fn := func(context.Context) map[string]string {
		if calls.Add(1) == 1 {
			close(started)
			<-release
			return map[string]string{"/repo": "1001"}
		}
		return map[string]string{"/repo": "1002"}
	}
	previous := discoverPortsImpl.Swap(&fn)
	t.Cleanup(func() { discoverPortsImpl.Store(previous); resetPortCache() })
	done := make(chan struct{})
	go func() { DiscoverOpenCodePortsContext(t.Context()); close(done) }()
	<-started
	InvalidateOpenCodePortCache()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if port := DiscoverOpenCodePortContext(ctx, "/repo"); port != "1002" {
		t.Fatalf("new lookup joined obsolete in-flight scan: %q", port)
	}
	unblock()
	<-done
	if port := DiscoverOpenCodePortContext(t.Context(), "/repo"); port != "1002" {
		t.Fatalf("old in-flight scan repopulated invalidated cache: %q", port)
	}
}
