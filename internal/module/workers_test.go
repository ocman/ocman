package module

import (
	"context"
	"sync/atomic"
	"testing"
)

func TestWorkersBindJoinAndRefuse(t *testing.T) {
	var w Workers
	ctx, cancel := context.WithCancel(t.Context())
	w.Bind(ctx)
	started := make(chan struct{})
	var done atomic.Bool
	w.Go(func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		done.Store(true)
	})
	<-started
	cancel()
	w.Close()
	if !done.Load() {
		t.Fatal("Close returned before the work did")
	}
	if w.Go(func(context.Context) { t.Error("ran after Close") }) {
		t.Fatal("accepted work after Close")
	}
}
