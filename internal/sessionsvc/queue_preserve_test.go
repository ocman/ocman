package sessionsvc

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/queuesvc"
	"github.com/ocman/ocman/internal/state/statetest"
)

type preservingQueueSender struct{ service *Service }

func (s preservingQueueSender) SendNow(ctx context.Context, platform string, req platforms.SendMessageRequest) error {
	return s.service.SendMessage(ctx, platform, req)
}
func (s preservingQueueSender) PrepareSend(ctx context.Context, platform string, req platforms.SendMessageRequest) (func(context.Context) error, error) {
	return s.service.PrepareSendMessage(ctx, platform, req)
}

type preservingQueueStatus struct{}

func (preservingQueueStatus) TurnRunning(context.Context, string, string) (bool, bool) {
	return false, true
}

func TestQueuedFollowupPreservesSelectionsAcrossRestart(t *testing.T) {
	for _, cooldown := range []bool{false, true} {
		t.Run(map[bool]string{false: "project-default", true: "provider-fallthrough"}[cooldown], func(t *testing.T) {
			store := statetest.Open(t)
			svc, p := newProjectModelService(map[string][]string{"/repo": {"default/model-b", "fallback/model-c"}})
			if cooldown {
				svc.RecordCooldown(t.Context(), "default", time.Hour)
			}
			sender := preservingQueueSender{svc}
			queue := queuesvc.New(store, sender, preservingQueueStatus{}, nil)
			// Decode the wire request so the unpatched code runs and demonstrates
			// the downstream substitution rather than failing at compilation.
			var req platforms.SendMessageRequest
			if err := json.Unmarshal([]byte(`{"SessionID":"session-a","Message":"Clarify the inventory","PreserveSelections":true}`), &req); err != nil {
				t.Fatal(err)
			}
			if err := queue.Enqueue(t.Context(), "opencode", true, req); err != nil {
				t.Fatal(err)
			}
			if len(p.sent) != 0 {
				t.Fatal("held message sent before restart")
			}
			queue = queuesvc.New(store, sender, preservingQueueStatus{}, nil)
			queue.Flush(t.Context(), "opencode", "session-a")
			if len(p.sent) != 1 {
				t.Fatalf("follow-up not delivered: %+v", p.sent)
			}
			// Empty selections leave runtime model A, agent and variant unchanged.
			// Any nonempty model would switch the existing session to model B/C.
			got := p.sent[0]
			currentModel := "session/model-a"
			if got.Model != "" {
				currentModel = got.Model
			}
			if currentModel != "session/model-a" || got.Agent != "" || got.Reasoning != "" {
				t.Fatalf("follow-up changed runtime selections: model=%s request=%+v", currentModel, got)
			}
		})
	}
}

func TestPreservedSelectionsRejectOverrides(t *testing.T) {
	svc, p := newProjectModelService(map[string][]string{"/repo": {"default/model-b"}})
	for _, req := range []platforms.SendMessageRequest{
		{Model: "provider/model"}, {Agent: "other-agent"}, {Reasoning: "other-variant"},
	} {
		req.SessionID, req.Message, req.PreserveSelections = "s", "follow up", true
		if err := svc.SendMessage(t.Context(), "opencode", req); err == nil {
			t.Fatalf("preserved selections accepted override: %+v", req)
		}
		store := statetest.Open(t)
		queue := queuesvc.New(store, preservingQueueSender{svc}, preservingQueueStatus{}, nil)
		if err := queue.Enqueue(t.Context(), "opencode", true, req); err == nil {
			t.Fatal("queue accepted conflicting selections")
		}
		if count, err := store.CountQueuedMessages(t.Context(), "opencode", "s"); err != nil || count != 0 {
			t.Fatalf("invalid selection persisted: %d %v", count, err)
		}
	}
	if len(p.sent) != 0 {
		t.Fatal("invalid selection was sent")
	}
}
