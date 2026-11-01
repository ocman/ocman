package acpexec

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

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
}

// One session's recording failure must not disconnect the shared adapter and
// fail every other session's turn.
func TestFailedUpdateIsScopedToItsSession(t *testing.T) {
	want := errors.New("event spool unavailable")
	var fail atomic.Bool
	fail.Store(true)
	c := startTestClient(t, "normal", Handlers{Update: func(context.Context, acp.SessionNotification) error {
		if fail.Load() {
			return want
		}
		return nil
	}})
	_, err := c.Prompt(testContext(t), acp.PromptRequest{SessionId: "native-session", Prompt: []acp.ContentBlock{acp.TextBlock("hello")}})
	if !errors.Is(err, want) {
		t.Fatalf("lost recording failure: %v", err)
	}
	select {
	case <-c.Done():
		t.Fatal("a session's recording failure disconnected the shared adapter")
	default:
	}
	// An update without a session cannot be scoped and ends the transport.
	var scoped, transport bool
	cb := callbacks{handlers: Handlers{Update: func(context.Context, acp.SessionNotification) error { return want }},
		failed: func(error) { transport = true }, sessionFailed: func(acp.SessionId, error) { scoped = true }}
	if err := cb.SessionUpdate(t.Context(), acp.SessionNotification{}); !errors.Is(err, want) || scoped || !transport {
		t.Fatalf("unscoped failure: %v scoped=%v transport=%v", err, scoped, transport)
	}
	fail.Store(false)
	result, err := c.Prompt(testContext(t), acp.PromptRequest{SessionId: "native-session", Prompt: []acp.ContentBlock{acp.TextBlock("again")}})
	if err != nil || result.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("recovered turn = %+v, %v", result, err)
	}
}

// The cancellation sent after a recording failure belongs to the failed
// prompt. It must not outlive that prompt and interrupt the session's next turn.
func TestFailureCancellationCannotCancelSuccessor(t *testing.T) {
	want := errors.New("event spool unavailable")
	successorStarted := make(chan struct{})
	c := startTestClient(t, "normal", Handlers{Update: func(_ context.Context, p acp.SessionNotification) error {
		if chunk := p.Update.AgentMessageChunk; chunk != nil && chunk.Content.Text != nil {
			switch chunk.Content.Text.Text {
			case "boom":
				return want
			case "started":
				close(successorStarted)
			}
		}
		return nil
	}})
	c.mu.Lock()
	// Delay the failure cancellation until the successor runs, or until a
	// correctly fenced client has shown it will not admit the successor first.
	c.beforeFailureCancel = func() {
		select {
		case <-successorStarted:
		case <-time.After(200 * time.Millisecond):
		}
	}
	c.mu.Unlock()
	ctx := testContext(t)
	if _, err := c.Prompt(ctx, acp.PromptRequest{SessionId: "native-session", Prompt: []acp.ContentBlock{acp.TextBlock("boom")}}); !errors.Is(err, want) {
		t.Fatalf("lost recording failure: %v", err)
	}
	type outcome struct {
		response acp.PromptResponse
		err      error
	}
	result := make(chan outcome, 1)
	go func() {
		response, err := c.Prompt(ctx, acp.PromptRequest{SessionId: "native-session", Prompt: []acp.ContentBlock{acp.TextBlock("wait")}})
		result <- outcome{response, err}
	}()
	select {
	case <-successorStarted:
	case <-ctx.Done():
		t.Fatal("successor never started")
	}
	select {
	case got := <-result:
		t.Fatalf("the failed prompt's cancellation ended its successor: %+v, %v", got.response, got.err)
	case <-time.After(500 * time.Millisecond):
	}
	if err := c.Cancel(ctx, "native-session"); err != nil {
		t.Fatal(err)
	}
	if got := <-result; got.err != nil || got.response.StopReason != acp.StopReasonCancelled {
		t.Fatalf("successor = %+v, %v", got.response, got.err)
	}
}

// A caller that gives up while a failure cancellation is outstanding is never
// admitted and leaves no operation behind.
func TestFencedAdmissionHonorsContext(t *testing.T) {
	c := &Client{sessions: map[acp.SessionId]*sessionState{}}
	fence := make(chan struct{})
	c.session("native-session").fence = fence
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if op, err := c.beginSessionOp(ctx, "native-session", true); op != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("admitted past the fence: %v, %v", op, err)
	}
	if s := c.sessions["native-session"]; s == nil || len(s.ops) != 0 {
		t.Fatalf("fenced session state = %+v", s)
	}
	admitted := make(chan *sessionOp, 1)
	go func() {
		op, _ := c.beginSessionOp(t.Context(), "native-session", true)
		admitted <- op
	}()
	select {
	case <-admitted:
		t.Fatal("admitted while the failure cancellation was outstanding")
	case <-time.After(50 * time.Millisecond):
	}
	c.mu.Lock()
	c.sessions["native-session"].fence = nil
	close(fence)
	c.mu.Unlock()
	if op := <-admitted; op == nil || !op.prompt {
		t.Fatalf("not admitted after the fence: %+v", op)
	}
}

// History replayed by LoadSession that cannot be recorded must fail the load,
// and a failure outside any operation must reach the next one rather than be
// silently discarded.
func TestRecordingFailuresReachSessionOperations(t *testing.T) {
	want := errors.New("event spool unavailable")
	c := startTestClient(t, "normal", Handlers{Update: func(_ context.Context, p acp.SessionNotification) error {
		if chunk := p.Update.AgentMessageChunk; chunk != nil && chunk.Content.Text != nil && chunk.Content.Text.Text == "history" {
			return want
		}
		return nil
	}})
	ctx := testContext(t)
	if _, err := c.LoadSession(ctx, acp.LoadSessionRequest{Cwd: t.TempDir(), SessionId: "native-session"}); !errors.Is(err, want) {
		t.Fatalf("load hid its recording failure: %v", err)
	}
	if _, err := c.Prompt(ctx, acp.PromptRequest{SessionId: "native-session", Prompt: []acp.ContentBlock{acp.TextBlock("hello")}}); err != nil {
		t.Fatalf("a reported load failure leaked into the next prompt: %v", err)
	}
	idle := errors.New("late update unavailable")
	c.failSessionUpdate("native-session", idle)
	if _, err := c.Prompt(ctx, acp.PromptRequest{SessionId: "native-session", Prompt: []acp.ContentBlock{acp.TextBlock("hello")}}); !errors.Is(err, idle) {
		t.Fatalf("prompt erased an unreported recording failure: %v", err)
	}
	if _, err := c.Prompt(ctx, acp.PromptRequest{SessionId: "native-session", Prompt: []acp.ContentBlock{acp.TextBlock("hello")}}); err != nil {
		t.Fatalf("reported failure was not consumed: %v", err)
	}
	c.failSessionUpdate("native-session", idle)
	if _, err := c.ResumeSession(ctx, acp.ResumeSessionRequest{Cwd: t.TempDir(), SessionId: "native-session"}); !errors.Is(err, idle) {
		t.Fatalf("resume erased an unreported recording failure: %v", err)
	}
	c.failSessionUpdate("native-session", idle)
	model := acp.SetSessionConfigOptionRequest{ValueId: &acp.SetSessionConfigOptionValueId{SessionId: "native-session", ConfigId: "model", Value: "m"}}
	if _, err := c.SetConfigOption(ctx, model); !errors.Is(err, idle) {
		t.Fatalf("config change erased an unreported recording failure: %v", err)
	}
	c.failSessionUpdate("native-session", idle)
	toggle := acp.SetSessionConfigOptionRequest{Boolean: &acp.SetSessionConfigOptionBoolean{SessionId: "native-session", ConfigId: "fast", Value: true}}
	if _, err := c.SetConfigOption(ctx, toggle); !errors.Is(err, idle) {
		t.Fatalf("config toggle erased an unreported recording failure: %v", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.sessions) != 0 {
		t.Fatalf("idle session state retained: %+v", c.sessions)
	}
}
