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

func (*fakeChildResultStore) GetChildSession(string) (*state.ChildSession, error) { return nil, nil }
func (*fakeChildResultStore) ListDisconnectedChildSessions(string) ([]state.ChildSession, error) {
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
func (f *fakeChildResultStore) SetChildResultDelivery(_ string, delivery string) error {
	f.delivery = delivery
	return nil
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

func TestAwaitChildResult_MarksCancelledWaitDisconnected(t *testing.T) {
	broker := NewChildResultBroker()
	broker.Register("child-1")
	store := &fakeChildResultStore{}
	notified := ""
	tools := &splitTools{
		results: broker,
		store:   store,
		disconnected: func(childID string) {
			notified = childID
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := tools.awaitChildResult(ctx, mcplib.CallToolRequest{}, "child-1", map[string]interface{}{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("await error = %v, want context.Canceled", err)
	}
	if store.delivery != "disconnected" {
		t.Fatalf("delivery = %q, want disconnected", store.delivery)
	}
	if notified != "child-1" {
		t.Fatalf("disconnect notification child = %q, want child-1", notified)
	}
}
