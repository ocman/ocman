package convstream

import (
	"context"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/convevents"
)

type acquisitionSource struct{ emitted chan struct{} }

func (s acquisitionSource) Watch(ctx context.Context, sink Sink) error {
	sink.Connected(Seed{})
	sink.Emit(textPart("p1", "on connect", true), Meta{})
	close(s.emitted)
	<-ctx.Done()
	return ctx.Err()
}

func TestAcquireRegistersWaiterBeforeSourceCanConnect(t *testing.T) {
	h := newTestHub(t, Options{})
	src := acquisitionSource{emitted: make(chan struct{})}
	w := &waiter{sub: make(chan *Subscription, 1)}
	s, err := h.acquire(testKey, func() Source { return src }, w)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.mu.Lock()
		s.dropWaiterLocked(w)
		s.mu.Unlock()
		h.release(s)
	})
	// Pause Subscribe at the acquisition boundary until the source has both
	// connected and emitted. Registration after acquire would be too late.
	select {
	case <-src.emitted:
	case <-time.After(waitFor):
		t.Fatal("source did not emit")
	}
	select {
	case sub := <-w.sub:
		w.sub <- sub // leave the unclaimed subscription for cleanup
		if hello := decode(t, sub.Hello.frame); hello.Seq != 0 {
			t.Fatalf("hello seq = %d, want before the first event", hello.Seq)
		}
		events := decodeAll(t, drain(sub))
		if len(events) != 1 || events[0].Event.EventType() != convevents.TypePartUpserted || events[0].Seq != 1 {
			t.Fatalf("events = %+v, want the event emitted on connect", events)
		}
	default:
		t.Fatal("acquire did not register the waiter before the source connected")
	}
}
