package codexprovider

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/codex/acptest"
	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
)

// These are the standard subagent updates emitted by codex-acp 2.5.0 over
// ACP v1. ACP child session IDs wrap native thread IDs, including for nesting.
func TestACPNativeSubagentRuntime(t *testing.T) {
	h, path, notify := subagentHarness(t)
	notify("thr", "thr:subagent:child", "")
	notify("thr", "thr:subagent:child", "running")
	assertChild(t, h, "child", "thr", db.StatusBusy)
	if _, ok := acptest.ReadState(path).ClientCapabilities["subagents"]; !ok {
		t.Fatal("native child sessions were not negotiated")
	}
	notify("thr:subagent:child", "thr:subagent:grandchild", "running")
	assertChild(t, h, "grandchild", "child", db.StatusBusy)
	// A native child is not a new root conversation that Ocman can resume.
	if err := h.b.acp.resume(t.Context(), h.b.acp.client, "child"); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatal(err)
	}
	if code := h.call(http.MethodGet, "/sessions?runtime=1&directory=/elsewhere", nil, new([]db.Session)); code != http.StatusOK {
		t.Fatal(code)
	}
	if rows := h.b.runtime("/elsewhere"); len(rows) != 0 {
		t.Fatal(rows)
	}
	for _, tc := range []struct {
		state string
		want  db.SessionStatus
	}{
		{"idle", db.StatusDone}, {"running", db.StatusBusy}, {"requires_action", db.StatusBusy}, {"unknown", db.StatusInterrupted},
	} {
		notify("thr", "thr:subagent:child", tc.state)
		assertChild(t, h, "child", "thr", tc.want)
	}
	if native, active := h.b.acp.interactionSession(t.Context(), "thr:subagent:grandchild"); native != "grandchild" || !active {
		t.Fatalf("interaction = %q %v", native, active)
	}
	if _, active := h.b.acp.interactionSession(t.Context(), "foreign"); active {
		t.Fatal("unknown interaction accepted")
	}
	// Child activity outlives its parent's foreground completion.
	sendACP(t, h, platforms.SendMessageRequest{Message: "hi"})
	h.next("session.idle", func(p map[string]any) bool { return p["sessionID"] == "thr" })
	assertChild(t, h, "grandchild", "child", db.StatusBusy)
	if err := h.b.acp.client.Close(); err != nil {
		t.Fatal(err)
	}
	h.next("session.idle", func(p map[string]any) bool { return p["sessionID"] == "grandchild" })
	assertChild(t, h, "grandchild", "child", db.StatusInterrupted)
}

func assertChild(t *testing.T, h *harness, id, parent string, status db.SessionStatus) {
	t.Helper()
	rows := h.b.runtime("")
	for _, row := range rows {
		if row.ID == id {
			if row.ParentID != parent || row.Status != status || !row.LiveConnection {
				t.Fatalf("child = %+v", row)
			}
			return
		}
	}
	t.Fatalf("native child %q missing from runtime: %+v", id, rows)
}

func subagentHarness(t *testing.T) (*harness, string, func(string, string, string)) {
	t.Helper()
	h, path := acpHarness(t)
	createACP(t, h)
	if _, _, err := h.b.conn(t.Context()); err != nil {
		t.Fatal(err)
	}
	thread := func(id string) codexappserver.Thread {
		parent := "thr"
		if id == "thr" {
			parent = ""
		}
		if id == "grandchild" {
			parent = "child"
		}
		return codexappserver.Thread{ID: id, SessionID: "thr", ParentThreadID: parent, Cwd: "/repo", CreatedAt: 1, UpdatedAt: 2}
	}
	h.fake().Handle(codexappserver.MethodThreadRead, func(raw json.RawMessage) (any, error) {
		var req struct {
			ID string `json:"threadId"`
		}
		_ = json.Unmarshal(raw, &req)
		return map[string]any{"thread": thread(req.ID)}, nil
	})
	h.fake().Handle(codexappserver.MethodThreadList, func(raw json.RawMessage) (any, error) {
		var req codexappserver.ThreadListParams
		_ = json.Unmarshal(raw, &req)
		rows := []codexappserver.Thread{}
		if req.Archived != nil && !*req.Archived && slices.Contains(req.SourceKinds, "subAgentThreadSpawn") {
			rows = []codexappserver.Thread{thread("thr"), thread("child"), thread("grandchild")}
		}
		return map[string]any{"data": rows}, nil
	})
	notify := func(parent, child, state string) {
		t.Helper()
		u := map[string]any{"sessionUpdate": "subagent_update", "sessionId": child, "title": "Review"}
		if state != "" {
			u["state"] = map[string]any{"state": state}
		}
		if _, err := h.b.acp.client.CallExtension(t.Context(), "_test/notify", map[string]any{"sessionId": parent, "update": u}); err != nil {
			t.Fatal(err)
		}
	}
	return h, path, notify
}
