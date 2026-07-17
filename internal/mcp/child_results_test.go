package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/state"
)

type fakeChildResultStore struct{ delivery string }

func (*fakeChildResultStore) GetChildSession(string) (*state.ChildSession, error) { return nil, nil }
func (*fakeChildResultStore) ListDisconnectedChildSessions(string) ([]state.ChildSession, error) {
	return nil, nil
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

	if err := tools.awaitChildResult(ctx, "child-1", map[string]interface{}{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("await error = %v, want context.Canceled", err)
	}
	if store.delivery != "disconnected" {
		t.Fatalf("delivery = %q, want disconnected", store.delivery)
	}
	if notified != "child-1" {
		t.Fatalf("disconnect notification child = %q, want child-1", notified)
	}
}
