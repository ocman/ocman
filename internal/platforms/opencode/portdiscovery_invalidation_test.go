package opencode

import (
	"context"
	"sync/atomic"
	"testing"
)

func TestInvalidationFencesInFlightPortScan(t *testing.T) {
	resetPortCache()
	started, release := make(chan struct{}), make(chan struct{})
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
	close(release)
	<-done
	if port := DiscoverOpenCodePortContext(t.Context(), "/repo"); port != "1002" {
		t.Fatalf("old in-flight scan repopulated invalidated cache: %q", port)
	}
}
