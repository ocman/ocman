package queuesvc

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
)

func TestSweepStopsAdmittingSessionsAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	store := &memStore{}
	sender := &recSender{onSend: cancel}
	svc := New(store, sender, statusStub{ok: true}, nil)
	for _, id := range []string{"first", "second"} {
		if err := svc.Enqueue(t.Context(), "opencode", true, platforms.SendMessageRequest{SessionID: id, Message: id}); err != nil {
			t.Fatal(err)
		}
	}
	svc.Sweep(ctx)
	if sent := sender.messages(); len(sent) != 1 || sent[0] != "first" {
		t.Fatalf("admitted after cancellation: %v", sent)
	}
	if rows, err := svc.List(t.Context(), "opencode", "second"); err != nil || len(rows) != 1 {
		t.Fatalf("later payload lost: %+v %v", rows, err)
	}
}

func TestCancelledLifecycleReadDoesNotSendOnIdleEdge(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	store, sender := &memStore{}, &recSender{}
	svc := New(store, sender, statusStub{ok: true, onLatest: cancel}, nil)
	if err := svc.Enqueue(t.Context(), "opencode", true, platforms.SendMessageRequest{SessionID: "s1", Message: "held"}); err != nil {
		t.Fatal(err)
	}
	svc.Flush(ctx, "opencode", "s1")
	if sent := sender.messages(); len(sent) != 0 {
		t.Fatal(sent)
	}
	if rows, err := svc.List(t.Context(), "opencode", "s1"); err != nil || len(rows) != 1 || rows[0].Attempts != 0 {
		t.Fatalf("payload altered: %+v %v", rows, err)
	}
}

type receiptStore struct {
	*memStore
	t *testing.T
}

func (s receiptStore) DeleteQueuedMessage(ctx context.Context, id string) (bool, error) {
	if ctx.Err() != nil {
		s.t.Error("dequeue receipt was cancelled")
	}
	return s.memStore.DeleteQueuedMessage(ctx, id)
}
func (s receiptStore) RecordQueuedMessageFailure(ctx context.Context, id, reason string) (bool, error) {
	if ctx.Err() != nil {
		s.t.Error("failure receipt was cancelled")
	}
	return s.memStore.RecordQueuedMessageFailure(ctx, id, reason)
}

type cancellingSender struct {
	cancel context.CancelFunc
	t      *testing.T
	err    error
}

func (s cancellingSender) SendNow(ctx context.Context, _ string, _ platforms.SendMessageRequest) error {
	s.cancel()
	if ctx.Err() != nil {
		s.t.Error("admitted delivery was cancelled")
	}
	return s.err
}

func TestAdmittedDeliveryFinishesItsReceipt(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "dequeue", true: "failure"}[fail], func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var sendErr error
			if fail {
				sendErr = errors.New("delivery failed")
			}
			store := receiptStore{&memStore{}, t}
			svc := New(store, cancellingSender{cancel, t, sendErr}, statusStub{ok: true}, nil)
			if err := svc.Enqueue(t.Context(), "opencode", true, platforms.SendMessageRequest{SessionID: "s1", Message: "held"}); err != nil {
				t.Fatal(err)
			}
			svc.Flush(ctx, "opencode", "s1")
			rows, err := svc.List(t.Context(), "opencode", "s1")
			if err != nil {
				t.Fatal(err)
			}
			if fail {
				if len(rows) != 1 || rows[0].Attempts != 1 || rows[0].LastError != sendErr.Error() {
					t.Fatalf("failure receipt missing: %+v", rows)
				}
			} else if len(rows) != 0 {
				t.Fatalf("delivery still replayable: %+v", rows)
			}
		})
	}
}
