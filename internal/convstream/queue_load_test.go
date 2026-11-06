package convstream

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/convevents"
)

func TestQueueRefreshBurstCoalescesSlowReads(t *testing.T) {
	for _, firstFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failed stale read"}[firstFails], func(t *testing.T) {
			var calls atomic.Int32
			started, release := make(chan struct{}), make(chan struct{})
			h := newTestHub(t, Options{Queue: func(context.Context, Key) ([]convevents.QueuedMessage, error) {
				n := calls.Add(1)
				if n == 1 {
					close(started)
					<-release
					if firstFails {
						return nil, errors.New("slow read failed")
					}
					return []convevents.QueuedMessage{{ID: "stale"}}, nil
				}
				return []convevents.QueuedMessage{{ID: "latest"}}, nil
			}})
			src := newFakeSource()
			sub := subscribe(t, h, src, "")
			sink := nextSink(t, src)
			sink.QueueChanged()
			<-started
			for range 99 {
				sink.QueueChanged()
			}
			close(release)
			events := await(t, sub, convevents.TypeQueueChanged)
			queue := events[len(events)-1].Event.(convevents.QueueChanged)
			if len(queue.Messages) != 1 || queue.Messages[0].ID != "latest" {
				t.Fatalf("stale queue published: %+v", queue)
			}
			h.Close()
			h.opts.Workers.Close()
			if got := calls.Load(); got != 2 {
				t.Fatalf("100 invalidations performed %d reads, want one in-flight plus one trailing read", got)
			}
		})
	}
}

func TestQueueRefreshStopsAfterDetach(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	h := newTestHub(t, Options{Queue: func(context.Context, Key) ([]convevents.QueuedMessage, error) {
		calls.Add(1)
		close(started)
		<-release
		return nil, nil
	}})
	src := newFakeSource()
	sub := subscribe(t, h, src, "")
	sink := nextSink(t, src)
	sink.QueueChanged()
	<-started
	sink.QueueChanged()
	h.Close()
	close(release)
	h.opts.Workers.Close()
	if calls.Load() != 1 || len(drain(sub)) != 0 {
		t.Fatal("detached session refreshed or published its queue")
	}
}

func TestQueueRefreshFailureAllowsLaterRefresh(t *testing.T) {
	var calls atomic.Int32
	h := newTestHub(t, Options{Queue: func(context.Context, Key) ([]convevents.QueuedMessage, error) {
		if calls.Add(1) == 1 {
			return nil, errors.New("failed read")
		}
		return nil, nil
	}})
	src := newFakeSource()
	sub := subscribe(t, h, src, "")
	sink := nextSink(t, src)
	sink.QueueChanged()
	deadline := time.Now().Add(waitFor)
	for {
		sub.s.mu.Lock()
		reading := sub.s.queueReading
		sub.s.mu.Unlock()
		if !reading {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("failed queue refresh did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	sink.QueueChanged()
	got := await(t, sub, convevents.TypeQueueChanged)
	queue := got[len(got)-1].Event.(convevents.QueueChanged)
	if queue.Messages == nil || len(queue.Messages) != 0 || calls.Load() != 2 {
		t.Fatalf("later refresh = %+v, reads=%d", queue, calls.Load())
	}
}

func TestQueueRefreshRefusesStoppedWorkers(t *testing.T) {
	h := newTestHub(t, Options{Queue: func(context.Context, Key) ([]convevents.QueuedMessage, error) {
		t.Error("stopped workers admitted a read")
		return nil, nil
	}})
	h.opts.Workers.Close()
	s := newSession(h, testKey)
	s.readQueueLocked()
	if s.queueReading {
		t.Fatal("refused refresh left queue reading flag set")
	}
	s.detached = true
	s.readQueueLocked()
}
