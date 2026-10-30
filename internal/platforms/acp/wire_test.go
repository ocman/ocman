package acpexec

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	acp "github.com/coder/acp-go-sdk"
	"github.com/ocman/ocman/internal/platforms"
)

func TestWireScopedElicitation(t *testing.T) {
	var received ElicitationRequest
	w := &wire{callbacks: callbacks{handlers: Handlers{Elicitation: func(_ context.Context, p ElicitationRequest) (acp.UnstableCreateElicitationResponse, error) {
		received = p
		return acp.UnstableCreateElicitationResponse{Cancel: &acp.UnstableCreateElicitationCancel{}}, nil
	}}}}
	raw := []byte(`{"mode":"form","sessionId":"session-b","toolCallId":"tool-b","message":"Question","requestedSchema":{"type":"object","properties":{}}}`)
	if _, err := w.handle(t.Context(), acp.ClientMethodElicitationCreate, raw); err != nil {
		t.Fatal(err)
	}
	if received.SessionID != "session-b" || received.ToolCallID != "tool-b" || received.Form.Message != "Question" {
		t.Fatalf("scope lost: %+v", received)
	}
	for _, raw := range []string{
		`{broken`,
		`{"mode":"form","message":"Q","requestedSchema":{"type":"object","properties":{}}}`,
		`{"mode":"url","sessionId":"s","message":"Q","url":"https://example.com","elicitationId":"e"}`,
	} {
		if _, err := w.handle(t.Context(), acp.ClientMethodElicitationCreate, []byte(raw)); err == nil {
			t.Fatalf("accepted unscoped/unsupported form: %s", raw)
		}
	}
	w.callbacks.handlers.Elicitation = nil
	if _, err := w.handle(t.Context(), acp.ClientMethodElicitationCreate, raw); err == nil || err.Code != -32601 {
		t.Fatal(err)
	}
}

func TestWireValidationAndHandlerErrors(t *testing.T) {
	w := &wire{callbacks: callbacks{handlers: Handlers{Update: func(context.Context, acp.SessionNotification) error { return errors.New("failed") }}}}
	for _, method := range []string{acp.ClientMethodSessionUpdate, acp.ClientMethodSessionRequestPermission} {
		if _, err := w.handle(t.Context(), method, []byte(`{broken`)); err == nil || err.Code != -32602 {
			t.Fatal(err)
		}
	}
	if _, err := w.handle(t.Context(), acp.ClientMethodSessionUpdate, []byte(`{"sessionId":"s","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"x"}}}`)); err == nil || err.Code != -32603 {
		t.Fatal(err)
	}
	permission := []byte(`{"sessionId":"s","toolCall":{"toolCallId":"t"},"options":[]}`)
	if _, err := w.handle(t.Context(), acp.ClientMethodSessionRequestPermission, permission); err != nil {
		t.Fatal(err)
	}
	w.callbacks.handlers.Permission = func(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
		return acp.RequestPermissionResponse{}, errors.Join(errors.New("outer"), acp.NewAuthRequired(nil))
	}
	if _, err := w.handle(t.Context(), acp.ClientMethodSessionRequestPermission, permission); err == nil || err.Code != -32000 {
		t.Fatal(err)
	}
	if err := decodeACP(json.RawMessage(`{}`), &invalidACP{}); err == nil {
		t.Fatal("validation error ignored")
	}
}

type invalidACP struct{}

func (*invalidACP) Validate() error { return errors.New("invalid") }

func TestSessionConfigForkAndExtensions(t *testing.T) {
	c := startTestClient(t, "normal", Handlers{})
	if _, err := c.SetConfigOption(testContext(t), acp.SetSessionConfigOptionRequest{ValueId: &acp.SetSessionConfigOptionValueId{SessionId: "s", ConfigId: "model", Value: "m"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CallExtension(testContext(t), "_session/test", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CallExtension(testContext(t), "session/test", nil); err == nil {
		t.Fatal("non-extension method accepted")
	}
	result, err := c.ForkSession(testContext(t), acp.UnstableForkSessionRequest{SessionId: "s", Cwd: t.TempDir()})
	if err != nil || result.SessionId != "forked" {
		t.Fatalf("%+v %v", result, err)
	}
	if _, err := c.ForkSession(testContext(t), acp.UnstableForkSessionRequest{Cwd: "relative"}); err == nil {
		t.Fatal("relative fork accepted")
	}
	c = startTestClient(t, "no-capabilities", Handlers{})
	if _, err := c.ForkSession(testContext(t), acp.UnstableForkSessionRequest{}); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatal(err)
	}
}
