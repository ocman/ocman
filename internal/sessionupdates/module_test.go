package sessionupdates

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
)

func TestUpdatesCoalesceBeforeReadingAndBoundAdmission(t *testing.T) {
	reads := 0
	var published []Update
	m := New(Deps{Read: func(_ context.Context, key Key) (*db.Session, error) {
		reads++
		return &db.Session{ID: key.SessionID}, nil
	}, Publish: func(u Update) { published = append(published, u) }})
	key := Key{"owner:test", "one"}
	if m.Submit(key, 1) {
		t.Fatal("accepted before startup")
	}
	if err := m.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	m.Routes(nil)
	for i := 1; i <= 100; i++ {
		if !m.Submit(key, uint64(i)) {
			t.Fatal("rejected duplicate")
		}
	}
	m.flush(t.Context())
	if reads != 1 || len(published) != 1 || published[0].Revision != 100 || published[0].Row.ID != "one" {
		t.Fatalf("reads=%d published=%v", reads, published)
	}
	for i := 0; i < 128; i++ {
		if !m.Submit(Key{"owner:test", fmt.Sprint(i)}, 1) {
			t.Fatal("premature overflow")
		}
	}
	if m.Submit(Key{"owner:test", "overflow"}, 1) {
		t.Fatal("unbounded admission")
	}
	if !m.Submit(Key{"owner:test", "0"}, 2) {
		t.Fatal("duplicate refused at capacity")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	m.flush(ctx)
	if reads != 1 {
		t.Fatal("read after cancellation")
	}
}

func TestUpdateFailurePublishesInvalidationAndShutdownJoins(t *testing.T) {
	got := make(chan Update, 1)
	m := New(Deps{Read: func(ctx context.Context, _ Key) (*db.Session, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("unbounded read")
		}
		return &db.Session{ID: "discard"}, errors.New("unavailable")
	}, Publish: func(u Update) { got <- u }})
	if err := m.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); m.Run(ctx) }()
	if !m.Submit(Key{"p", "s"}, 1) {
		t.Fatal("rejected")
	}
	select {
	case u := <-got:
		if u.Row != nil || u.Revision != 1 {
			t.Fatal(u)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no invalidation")
	}
	cancel()
	<-done
	if m.Submit(Key{"p", "s"}, 2) {
		t.Fatal("accepted after shutdown")
	}
}
