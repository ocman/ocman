package worker

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDrainWaitsForWorkEnqueuedWhileHandlerRuns(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	processed := make(chan int, 2)
	w := New(func(v int) {
		if v == 1 {
			close(started)
			<-release
		}
		processed <- v
	})

	w.Enqueue(1)
	<-started
	drained := make(chan error, 1)
	go func() { drained <- w.Drain(context.Background()) }()
	w.Enqueue(2)

	select {
	case err := <-drained:
		t.Fatalf("Drain returned while work was in flight: %v", err)
	default:
	}
	close(release)
	select {
	case err := <-drained:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Drain did not finish")
	}
	if first, second := <-processed, <-processed; first != 1 || second != 2 {
		t.Fatalf("processed = [%d %d], want [1 2]", first, second)
	}
}

func TestDrainHonorsContext(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	w := New(func(struct{}) {
		close(started)
		<-release
	})
	w.Enqueue(struct{}{})
	<-started

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := w.Drain(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Drain error = %v, want context.Canceled", err)
	}
	close(release)
}

func TestKeyedWorkerSerializesSameKey(t *testing.T) {
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondStarted := make(chan struct{})
	w := NewKeyed(func(v int) {
		if v == 1 {
			close(firstStarted)
			<-releaseFirst
			return
		}
		close(secondStarted)
	}, func(int) string { return "same" })

	w.Enqueue(1)
	w.Enqueue(2)
	<-firstStarted
	select {
	case <-secondStarted:
		t.Fatal("same-key work ran concurrently")
	default:
	}
	close(releaseFirst)
	if err := w.Drain(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-secondStarted:
	default:
		t.Fatal("second same-key item did not run")
	}
}
