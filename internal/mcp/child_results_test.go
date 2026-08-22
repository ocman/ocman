package mcp

import (
	"context"
	"errors"
	"testing"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"

	"github.com/NoUseFreak/ocman/internal/state"
)

type fakeChildResultStore struct{ delivery string }

func (*fakeChildResultStore) GetChildSession(context.Context, string) (*state.ChildSession, error) {
	return nil, nil
}
func (*fakeChildResultStore) ListDisconnectedChildSessions(context.Context, string) ([]state.ChildSession, error) {
	return nil, nil
}

func TestRunChildResultProgress_EmitsPeriodically(t *testing.T) {
	done := make(chan struct{})
	progress := make(chan int, 3)
	go runChildResultProgress(context.Background(), done, time.Millisecond, func(step int) {
		progress <- step
	})

	for want := 1; want <= 2; want++ {
		select {
		case got := <-progress:
			if got != want {
				t.Fatalf("progress step = %d, want %d", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for progress step %d", want)
		}
	}
	close(done)
}
func (f *fakeChildResultStore) CompareAndSetChildResultDelivery(_ context.Context, _ string, from, to string) (bool, error) {
	if f.delivery != from {
		return false, nil
	}
	f.delivery = to
	return true, nil
}

type blockingChildResultStore struct {
	fakeChildResultStore
	transitioning chan struct{}
	resume        chan struct{}
}

func (f *blockingChildResultStore) CompareAndSetChildResultDelivery(_ context.Context, id, from, to string) (bool, error) {
	close(f.transitioning)
	<-f.resume
	return f.fakeChildResultStore.CompareAndSetChildResultDelivery(context.Background(), id, from, to)
}

func TestChildResultBroker_CancelDetachesWaiter(t *testing.T) {
	broker := NewChildResultBroker()
	broker.Register("child-1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := broker.Wait(ctx, "child-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait error = %v, want context.Canceled", err)
	}
	if broker.Deliver("child-1", ChildResult{Status: "completed"}) {
		t.Fatal("cancelled waiter still claimed child result")
	}
}

func TestChildResultBroker_Unregister(t *testing.T) {
	broker := NewChildResultBroker()
	broker.Register("child-1")
	broker.Unregister("child-1")
	if broker.Deliver("child-1", ChildResult{Status: "completed"}) {
		t.Fatal("unregistered waiter claimed child result")
	}
}

func TestChildResultBroker_ReportsRegistration(t *testing.T) {
	broker := NewChildResultBroker()
	if broker.Registered("child-1") {
		t.Fatal("unregistered child reported active")
	}
	broker.Register("child-1")
	if !broker.Registered("child-1") {
		t.Fatal("registered child reported inactive")
	}
}

func TestChildResultBroker_RegisterDoesNotReplaceWaiter(t *testing.T) {
	broker := NewChildResultBroker()
	if !broker.Register("child-1") {
		t.Fatal("first waiter was rejected")
	}
	if broker.Register("child-1") {
		t.Fatal("second waiter replaced the first")
	}
	if !broker.Deliver("child-1", ChildResult{Status: "completed"}) {
		t.Fatal("original waiter was stranded")
	}
	result, err := broker.Wait(context.Background(), "child-1")
	if err != nil || result.Status != "completed" {
		t.Fatalf("original waiter result = %+v, %v", result, err)
	}
}

func TestAwaitChildResult_MarksCancelledWaitDisconnected(t *testing.T) {
	broker := NewChildResultBroker()
	broker.Register("child-1")
	store := &fakeChildResultStore{delivery: "waiting"}
	notified := ""
	tools := &splitTools{
		results: broker,
		store:   store,
		disconnected: func(_ context.Context, childID string) {
			notified = childID
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := awaitChildResult(ctx, mcplib.CallToolRequest{}, "child-1", map[string]interface{}{}, tools.results, tools.store, tools.disconnected); !errors.Is(err, context.Canceled) {
		t.Fatalf("await error = %v, want context.Canceled", err)
	}
	if store.delivery != "disconnected" {
		t.Fatalf("delivery = %q, want disconnected", store.delivery)
	}
	if notified != "child-1" {
		t.Fatalf("disconnect notification child = %q, want child-1", notified)
	}
}

// #458: a result already received from the channel must be returned even
// when the context is cancelled in the same scheduling window. Discarding
// it (the old post-receive ctx.Err() check) lost the payload after the
// waiter had been removed — the parent saw a timeout for a completed child.
func TestWaitReturnsBufferedResultOverCancellation(t *testing.T) {
	broker := NewChildResultBroker()
	if !broker.Register("child-1") {
		t.Fatal("registering waiter")
	}
	if !broker.Deliver("child-1", ChildResult{Status: "completed", Summary: "done"}) {
		t.Fatal("delivering result")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled: the result is buffered and must still win

	result, err := broker.Wait(ctx, "child-1")
	if err != nil {
		t.Fatalf("Wait = %v; want the buffered result despite cancellation", err)
	}
	if result.Status != "completed" || result.Summary != "done" {
		t.Fatalf("result = %+v; want the delivered payload", result)
	}
}

func TestAwaitChildResultKeepsWaiterRegisteredUntilStateTransition(t *testing.T) {
	broker := NewChildResultBroker()
	if !broker.Register("child-1") {
		t.Fatal("registering waiter")
	}
	store := &blockingChildResultStore{
		fakeChildResultStore: fakeChildResultStore{delivery: "waiting"},
		transitioning:        make(chan struct{}),
		resume:               make(chan struct{}),
	}
	done := make(chan error, 1)
	go func() {
		done <- awaitChildResult(context.Background(), mcplib.CallToolRequest{}, "child-1", map[string]interface{}{}, broker, store, nil)
	}()
	if !broker.Deliver("child-1", ChildResult{Status: "completed"}) {
		t.Fatal("delivering result")
	}
	<-store.transitioning
	if broker.Register("child-1") {
		t.Fatal("next-turn waiter entered before delivery state committed")
	}
	close(store.resume)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !broker.Register("child-1") {
		t.Fatal("waiter was not released after delivery state committed")
	}
}
