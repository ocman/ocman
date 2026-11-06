package queuesvc

import (
	"context"
	"github.com/ocman/ocman/internal/platforms"
	"testing"
)

func TestSetupGateHoldsEveryDrainUntilFirstDelivery(t *testing.T) {
	for _, path := range []string{"idle", "sweep", "setup-completion"} {
		t.Run(path, func(t *testing.T) {
			sender := &recSender{}
			svc := New(&memStore{}, sender, statusStub{ok: true}, nil)
			blocked := true
			svc.WithStartGate(func(_ context.Context, id string) bool { return id == "draft" && blocked })
			if err := svc.Enqueue(t.Context(), "opencode", true, platforms.SendMessageRequest{SessionID: "draft", Message: "follow-up"}); err != nil {
				t.Fatal(err)
			}
			drain := func() {
				switch path {
				case "idle":
					svc.Flush(t.Context(), "opencode", "draft")
				case "sweep":
					svc.Sweep(t.Context())
				default:
					svc.TryFlush(t.Context(), "opencode", "draft")
				}
			}
			drain()
			if len(sender.messages()) != 0 {
				t.Fatal("follow-up overtook the first prompt")
			}
			blocked = false
			drain()
			if got := sender.messages(); len(got) != 1 || got[0] != "follow-up" {
				t.Fatal(got)
			}
			drain()
			if len(sender.messages()) != 1 {
				t.Fatal("follow-up sent twice")
			}
		})
	}
}

func TestSetupCompletionDoesNotPretendFirstTurnIsIdle(t *testing.T) {
	sender := &recSender{}
	svc := New(&memStore{}, sender, statusStub{ok: true, running: true}, nil)
	if err := svc.Enqueue(t.Context(), "opencode", true, platforms.SendMessageRequest{SessionID: "draft", Message: "follow-up"}); err != nil {
		t.Fatal(err)
	}
	svc.TryFlush(t.Context(), "opencode", "draft")
	if len(sender.messages()) != 0 {
		t.Fatal("setup completion interrupted the first turn")
	}
	svc.Flush(t.Context(), "opencode", "draft")
	if len(sender.messages()) != 1 {
		t.Fatal("idle edge did not release the follow-up")
	}
}
