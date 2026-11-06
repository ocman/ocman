package codexprovider

import (
	"context"
	"net/http"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/providerprotocol"
)

func TestACPChildFailedRunningWriteCannotSurviveDisconnect(t *testing.T) {
	h, _, notify := subagentHarness(t)
	notify("thr", "thr:subagent:child", "idle")
	restore := breakSpool(t, h)
	err := h.b.acp.subagent(t.Context(), childNotification("thr", "thr:subagent:child", "running", ""))
	if err == nil {
		t.Fatal("failed running publication was acknowledged")
	}
	assertChild(t, h, "child", "thr", db.StatusDone) // The persisted status still lags.
	if err := h.b.acp.client.Close(); err != nil {
		t.Fatal(err)
	}
	h.b.acp.retireChildren(h.b.acp.generation)
	restore()
	h.next("session.status", func(p map[string]any) bool {
		return p["sessionID"] == "child" && p["status"].(map[string]any)["type"] == "interrupted"
	})
	h.next("session.idle", func(p map[string]any) bool { return p["sessionID"] == "child" })
	assertChild(t, h, "child", "thr", db.StatusInterrupted)
	assertChildDrainIdle(t, h)
}

func TestACPDisconnectedChildPromptsRecoverBeforeReplacement(t *testing.T) {
	for _, question := range []bool{false, true} {
		name := "permission"
		if question {
			name = "question"
		}
		t.Run(name, func(t *testing.T) {
			h, _, notify := subagentHarness(t)
			notify("thr", "thr:subagent:child", "running")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan struct{})
			if question {
				req := formRequest(t, `{"type":"object","properties":{"choice":{"type":"string"}}}`)
				req.SessionID = "thr:subagent:child"
				go func() { _, _ = h.b.acp.question(ctx, req); close(done) }()
			} else {
				go func() {
					_, _ = h.b.acp.permission(ctx, acp.RequestPermissionRequest{SessionId: "thr:subagent:child", ToolCall: acp.ToolCallUpdate{ToolCallId: "child-tool"}})
					close(done)
				}()
			}
			asked := h.next(name+".asked", nil)
			id := asked["id"].(string)
			restore := breakSpool(t, h)
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("child callback did not cancel")
			}
			h.b.mu.Lock()
			pending := h.b.prompts[id] != nil
			h.b.mu.Unlock()
			if !pending {
				t.Fatal("unrecorded child prompt disappeared")
			}
			previous := h.b.acp.client
			if err := previous.Close(); err != nil {
				t.Fatal(err)
			}
			// Close reaps the process; the transport observes disconnect separately.
			// conn must see Done before this test can require replacement.
			select {
			case <-previous.Done():
			case <-time.After(5 * time.Second):
				t.Fatal("closed ACP transport did not disconnect")
			}
			// Connection replacement must finish old-generation prompt cleanup
			// before it discards the only native child mapping.
			reconnected := make(chan error, 1)
			go func() { _, err := h.b.acp.conn(t.Context()); reconnected <- err }()
			restore()
			select {
			case err := <-reconnected:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("child cleanup did not recover")
			}
			if h.b.acp.client == previous {
				t.Fatal("disconnected ACP connection was reused")
			}
			h.b.mu.Lock()
			pending = h.b.prompts[id] != nil
			h.b.mu.Unlock()
			if pending {
				t.Fatal("disconnected child prompt survived spool recovery and connection replacement")
			}
			assertChild(t, h, "child", "thr", db.StatusInterrupted)
			assertChildDrainIdle(t, h)
		})
	}
}

func assertChildDrainIdle(t *testing.T, h *harness) {
	t.Helper()
	var state providerprotocol.DrainState
	if code := h.call(http.MethodPost, "/drain", nil, &state); code != http.StatusOK || !state.Idle {
		t.Fatalf("child prevented drain: status=%d state=%+v", code, state)
	}
}
