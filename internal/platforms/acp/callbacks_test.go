package acpexec

import (
	"context"
	"errors"
	"testing"

	acp "github.com/coder/acp-go-sdk"
)

func TestPermissionRoundTrip(t *testing.T) {
	for _, tt := range []struct {
		name      string
		fn        func(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error)
		want      acp.StopReason
		wantError bool
	}{
		{name: "missing handler cancels", want: acp.StopReasonCancelled},
		{name: "allow", want: acp.StopReasonEndTurn, fn: func(_ context.Context, p acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
			if p.SessionId != "native-session" || p.ToolCall.ToolCallId != "tool-1" {
				return acp.RequestPermissionResponse{}, errors.New("lost request identity")
			}
			return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected(p.Options[0].OptionId)}, nil
		}},
		{name: "cancel", want: acp.StopReasonCancelled, fn: func(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
			return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, nil
		}},
		{name: "unknown option rejected", wantError: true, fn: func(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
			return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected("not-offered")}, nil
		}},
		{name: "invalid outcome rejected", wantError: true, fn: func(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
			return acp.RequestPermissionResponse{}, nil
		}},
		{name: "handler failure", wantError: true, fn: func(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
			return acp.RequestPermissionResponse{}, errors.New("permission storage unavailable")
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := startTestClient(t, "normal", Handlers{Permission: tt.fn})
			result, err := c.Prompt(testContext(t), acp.PromptRequest{SessionId: "native-session", Prompt: []acp.ContentBlock{acp.TextBlock("permission")}})
			if (err != nil) != tt.wantError || (!tt.wantError && result.StopReason != tt.want) {
				t.Fatalf("result %+v, error %v", result, err)
			}
		})
	}
}

func TestCancelledPermissionNeverApproves(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	called := false
	c := callbacks{handlers: Handlers{Permission: func(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
		called = true
		cancel()
		return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected("allow")}, nil
	}}}
	result, err := c.RequestPermission(ctx, acp.RequestPermissionRequest{})
	if err != nil || !called || result.Outcome.Cancelled == nil {
		t.Fatalf("late approval accepted: %+v, %v", result, err)
	}
	called = false
	result, err = c.RequestPermission(ctx, acp.RequestPermissionRequest{})
	if err != nil || called || result.Outcome.Cancelled == nil {
		t.Fatalf("cancelled handler called: %+v, %v", result, err)
	}
}

func TestHostCallbacksFailClosed(t *testing.T) {
	w := &wire{}
	for _, method := range []string{"fs/read_text_file", "fs/write_text_file", "terminal/create", "terminal/kill", "terminal/output", "terminal/release", "terminal/wait_for_exit"} {
		_, rpc := w.handle(t.Context(), method, []byte(`{}`))
		if rpc == nil || rpc.Code != -32601 {
			t.Fatalf("host callback did not reject %s: %v", method, rpc)
		}
	}
}

func TestFailedUpdateCannotCompleteSuccessfully(t *testing.T) {
	want := errors.New("event spool unavailable")
	c := startTestClient(t, "normal", Handlers{Update: func(context.Context, acp.SessionNotification) error { return want }})
	_, err := c.Prompt(testContext(t), acp.PromptRequest{SessionId: "native-session", Prompt: []acp.ContentBlock{acp.TextBlock("hello")}})
	if !errors.Is(err, want) {
		t.Fatalf("lost recording failure: %v", err)
	}
	select {
	case <-c.Done():
	case <-testContext(t).Done():
		t.Fatal("connection kept running after recording failure")
	}
}
