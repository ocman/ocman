package codexprovider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	acpexec "github.com/ocman/ocman/internal/platforms/acp"
	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
)

func childNotification(parent, child, state, stop string) acpexec.SubagentNotification {
	return acpexec.SubagentNotification{SessionID: parent, Update: acpexec.SubagentUpdate{SessionID: child,
		State: &acpexec.SubagentState{State: state, StopReason: stop}}}
}

func TestACPSubagentIdentityFailsClosed(t *testing.T) {
	h, _, notify := subagentHarness(t)
	for _, tc := range []struct{ parent, child string }{
		{"foreign", "thr:subagent:child"},
		{"thr", "child"},
		{"thr", "foreign:subagent:child"},
		{"thr", "thr:subagent:thr"},
		{"thr", "thr:subagent:%xx"},
		{"thr", "%xx:subagent:child"},
		{"thr", "thr:subagent:"},
		{"thr", "thr:subagent:grandchild"}, // Native parent is child, not thr.
	} {
		if err := h.b.acp.subagent(t.Context(), childNotification(tc.parent, tc.child, "running", "")); err == nil {
			t.Fatalf("accepted foreign or malformed ancestry: %+v", tc)
		}
	}
	notify("thr", "thr:subagent:child", "running")
	notify("thr:subagent:child", "thr:subagent:grandchild", "running")
	if err := h.b.acp.subagent(t.Context(), childNotification("thr", "thr:subagent:grandchild", "running", "")); err == nil {
		t.Fatal("accepted changed parent")
	}
	if err := h.b.acp.subagent(t.Context(), childNotification("thr", "thr:subagent:child", "new-unknown-state", "")); err == nil {
		t.Fatal("accepted unknown lifecycle")
	}
	for _, tc := range []struct {
		stop string
		want db.SessionStatus
	}{{"error", db.StatusError}, {"cancelled", db.StatusInterrupted}, {"end_turn", db.StatusDone}} {
		if err := h.b.acp.subagent(t.Context(), childNotification("thr", "thr:subagent:child", "idle", tc.stop)); err != nil {
			t.Fatal(err)
		}
		assertChild(t, h, "child", "thr", tc.want)
	}
	// A stale transport retirement must not settle a new writer's observations.
	notify("thr", "thr:subagent:child", "running")
	h.b.acp.retireChildren(0)
	assertChild(t, h, "child", "thr", db.StatusBusy)
}

func TestACPSubagentReaderAndSpoolFailures(t *testing.T) {
	for _, stage := range []string{"read", "metadata", "lifecycle"} {
		t.Run(stage, func(t *testing.T) {
			h, _, notify := subagentHarness(t)
			if stage == "read" {
				h.fake().Handle(codexappserver.MethodThreadRead, func(json.RawMessage) (any, error) { return nil, errors.New("history unavailable") })
			} else {
				if stage == "lifecycle" {
					notify("thr", "thr:subagent:child", "running")
				}
				backup := h.spool + ".saved"
				if err := os.Rename(h.spool, backup); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(h.spool, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Remove(h.spool); _ = os.Rename(backup, h.spool) })
			}
			err := h.b.acp.subagent(t.Context(), childNotification("thr", "thr:subagent:child", "idle", "end_turn"))
			if err == nil {
				t.Fatal("failure reported as a recorded child lifecycle")
			}
		})
	}
}

func TestACPChildInteractionsUseNativeIdentity(t *testing.T) {
	h, _, _ := subagentHarness(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	// Inbound requests are concurrent in the ACP SDK. A permission can overtake
	// the queued announcement while its native parent is still being verified.
	reading, release := make(chan struct{}), make(chan struct{})
	h.fake().Handle(codexappserver.MethodThreadRead, func(json.RawMessage) (any, error) {
		close(reading)
		<-release
		return map[string]any{"thread": codexappserver.Thread{ID: "child", ParentThreadID: "thr", Cwd: "/repo"}}, nil
	})
	announced := make(chan error, 1)
	go func() {
		announced <- h.b.acp.subagent(ctx, childNotification("thr", "thr:subagent:child", "running", ""))
	}()
	select {
	case <-reading:
	case err := <-announced:
		t.Fatalf("child announcement failed before its metadata read: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	permission := make(chan acp.RequestPermissionResponse, 1)
	requested := make(chan struct{})
	go func() {
		close(requested)
		response, _ := h.b.acp.permission(ctx, acp.RequestPermissionRequest{SessionId: "thr:subagent:child",
			ToolCall: acp.ToolCallUpdate{ToolCallId: "child-tool", RawInput: map[string]any{"command": "pwd"}},
			Options:  []acp.PermissionOption{{OptionId: "allow", Name: "Allow", Kind: acp.PermissionOptionKindAllowOnce}}})
		permission <- response
	}()
	<-requested
	close(release)
	if err := <-announced; err != nil {
		t.Fatal(err)
	}
	request := h.next("permission.asked", nil)
	if request["sessionID"] != "child" {
		t.Fatal(request)
	}
	if code := h.call(http.MethodPost, "/permissions/"+request["id"].(string)+"/reply", platforms.RespondPermissionRequest{Reply: "once"}, nil); code != http.StatusNoContent {
		t.Fatal(code)
	}
	if response := <-permission; response.Outcome.Selected == nil {
		t.Fatal(response)
	}
	var req acp.UnstableCreateElicitationRequest
	if err := json.Unmarshal([]byte(`{"mode":"form","message":"Choose","requestedSchema":{"type":"object","properties":{"choice":{"type":"string"}}}}`), &req); err != nil {
		t.Fatal(err)
	}
	question := make(chan acp.UnstableCreateElicitationResponse, 1)
	go func() {
		response, _ := h.b.acp.question(ctx, acpexec.ElicitationRequest{SessionID: "thr:subagent:child", ToolCallID: "question", Form: req.Form})
		question <- response
	}()
	request = h.next("question.asked", nil)
	if request["sessionID"] != "child" {
		t.Fatal(request)
	}
	if code := h.call(http.MethodPost, "/questions/"+request["id"].(string)+"/reply", platforms.RespondQuestionRequest{Answers: [][]string{{"yes"}}}, nil); code != http.StatusNoContent {
		t.Fatal(code)
	}
	if response := <-question; response.Accept == nil {
		t.Fatal(response)
	}
}

func TestACPSubagentCannotBeResumedAfterReconnect(t *testing.T) {
	h, _, _ := subagentHarness(t)
	// No runtime child map exists here. Native metadata must still prevent
	// adopting a historical child as an independently executable root.
	if err := h.b.acp.resume(t.Context(), h.b.acp.client, "child"); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatal(err)
	}
	for _, call := range h.fake().Calls() {
		if call.Method == "thread/resume" {
			t.Fatal("native reader resumed a child")
		}
	}
}

func TestACPResumeRestoresNativeChildBeforeResponse(t *testing.T) {
	t.Setenv("FAKE_CODEX_ACP_RESUME_CHILD", "1")
	h, _, _ := subagentHarness(t)
	h.b.acp.mu.Lock()
	delete(h.b.acp.loaded, "thr")
	h.b.acp.mu.Unlock()
	if err := h.b.acp.resume(t.Context(), h.b.acp.client, "thr"); err != nil {
		t.Fatal(err)
	}
	assertChild(t, h, "child", "thr", db.StatusBusy)
}

func TestACPUnannouncedChildInteractionCancels(t *testing.T) {
	h, _, _ := subagentHarness(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, active := h.b.acp.interactionSession(ctx, "thr:subagent:unannounced"); active {
		t.Fatal("unproved child became actionable")
	}
}
