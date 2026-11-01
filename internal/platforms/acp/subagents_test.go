package acpexec

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	acp "github.com/coder/acp-go-sdk"
)

func TestSubagentNegotiationAndNotification(t *testing.T) {
	var notifications []SubagentNotification
	c := startTestClient(t, "subagents", Handlers{
		Subagent: func(_ context.Context, p SubagentNotification) error {
			notifications = append(notifications, p)
			return nil
		},
		Elicitation: func(context.Context, ElicitationRequest) (acp.UnstableCreateElicitationResponse, error) {
			return acp.UnstableCreateElicitationResponse{}, nil
		},
	})
	if _, err := c.Prompt(testContext(t), acp.PromptRequest{SessionId: "native-session", Prompt: []acp.ContentBlock{acp.TextBlock("subagent")}}); err != nil {
		t.Fatal(err)
	}
	if len(notifications) != 1 || notifications[0].Update.State.State != "running" {
		t.Fatalf("lost subagent notification: %+v", notifications)
	}
	w := &wire{callbacks: callbacks{handlers: Handlers{Subagent: func(context.Context, SubagentNotification) error { return nil }}}}
	if _, err := w.Initialize(t.Context(), acp.InitializeRequest{Meta: map[string]any{"invalid": make(chan struct{})}}); err == nil {
		t.Fatal("accepted unencodable initialization metadata")
	}
}

func TestSubagentWireRetainsParentAndLifecycle(t *testing.T) {
	want := errors.New("spool unavailable")
	failed := false
	w := &wire{callbacks: callbacks{handlers: Handlers{Subagent: func(_ context.Context, p SubagentNotification) error {
		if p.SessionID != "root:subagent:child" || p.Update.SessionID != "root:subagent:grandchild" || p.Update.Title == nil || *p.Update.Title != "Review" || p.Update.State.State != "idle" || p.Update.State.StopReason != "error" {
			t.Fatalf("lost subagent notification fields: %+v", p)
		}
		return want
	}}, failed: func(err error) { failed = errors.Is(err, want) }}}
	_, err := w.handle(t.Context(), "session/update", json.RawMessage(`{"sessionId":"root:subagent:child","update":{"sessionUpdate":"subagent_update","sessionId":"root:subagent:grandchild","title":"Review","state":{"state":"idle","stopReason":"error"}}}`))
	if err == nil || !failed {
		t.Fatalf("sink failure was not propagated: %v %v", err, failed)
	}
}

func TestSubagentWireRejectsMalformedUpdates(t *testing.T) {
	w := &wire{callbacks: callbacks{handlers: Handlers{Subagent: func(context.Context, SubagentNotification) error {
		t.Fatal("invalid child identity reached the handler")
		return nil
	}}}}
	for _, raw := range []string{
		`{`,
		`{"sessionId":"root","update":{"sessionUpdate":"subagent_update"}}`,
		`{"update":{"sessionUpdate":"subagent_update","sessionId":"child"}}`,
		`{"sessionId":"root","update":{"sessionUpdate":"subagent_update","sessionId":123}}`,
	} {
		if _, err := w.handle(t.Context(), "session/update", json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
