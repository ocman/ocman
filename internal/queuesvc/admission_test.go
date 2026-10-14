package queuesvc

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestAdmissionRejectsNewQueuedWorkWithoutDroppingExistingQueue(t *testing.T) {
	store, sender := &memStore{}, &recSender{}
	svc := New(store, sender, statusStub{ok: true}, nil)
	req := platforms.SendMessageRequest{SessionID: "s", Message: "existing"}
	if err := svc.Enqueue(t.Context(), "opencode", true, req); err != nil {
		t.Fatal(err)
	}
	svc.WithAdmission(func(context.Context, string) (func(), error) { return nil, platforms.ErrBusy })
	if err := svc.Enqueue(t.Context(), "opencode", true, req); !errors.Is(err, platforms.ErrBusy) {
		t.Fatal(err)
	}
	svc.Flush(t.Context(), "opencode", "s")
	if got := sender.messages(); len(got) != 1 || got[0] != "existing" {
		t.Fatalf("existing work: %v", got)
	}
	finished := 0
	svc.WithAdmission(func(context.Context, string) (func(), error) { return func() { finished++ }, nil })
	if err := svc.Enqueue(t.Context(), "opencode", true, req); err != nil || finished != 1 {
		t.Fatalf("lease: %d %v", finished, err)
	}
}
