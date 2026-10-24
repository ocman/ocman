package opencode

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestInvalidationFencesInFlightPortScan(t *testing.T) {
	td.resetPortCache()
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
	previous := discoverPortsOverride.Swap(&fn)
	t.Cleanup(func() { discoverPortsOverride.Store(previous); td.resetPortCache() })
	done := make(chan struct{})
	go func() { td.DiscoverOpenCodePortsContext(t.Context()); close(done) }()
	<-started
	td.InvalidateOpenCodePortCache()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if port := td.DiscoverOpenCodePortContext(ctx, "/repo"); port != "1002" {
		t.Fatalf("new lookup joined obsolete in-flight scan: %q", port)
	}
	unblock()
	<-done
	if port := td.DiscoverOpenCodePortContext(t.Context(), "/repo"); port != "1002" {
		t.Fatalf("old in-flight scan repopulated invalidated cache: %q", port)
	}
}
