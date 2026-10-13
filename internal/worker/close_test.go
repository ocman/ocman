package worker

import (
	"context"
	"testing"
)

func TestCloseJoinsWorkAndDispatcher(t *testing.T) {
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	w := NewKeyed(func(_ string) { close(entered); <-release }, func(key string) string { return key })
	w.Enqueue("one")
	<-entered
	go func() { w.Close(); close(done) }()
	close(release)
	<-done
	select {
	case <-w.done:
	default:
		t.Fatal("dispatcher was not joined")
	}
	w.Enqueue("rejected")
	if err := w.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	w.Close() // Idempotent.
}
