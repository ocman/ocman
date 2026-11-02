package codexprovider

import (
	"errors"
	"os"
	"strings"
	"testing"

	acp "github.com/coder/acp-go-sdk"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

func breakSpool(t *testing.T, h *harness) func() {
	t.Helper()
	if err := os.Rename(h.spool, h.spool+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.spool, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return func() {
		if err := os.Remove(h.spool); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(h.spool+".saved", h.spool); err != nil {
			t.Fatal(err)
		}
	}
}

func (h *harness) start(id string) {
	h.t.Helper()
	if err := h.b.sup.BeginTurn(id, func() {
		h.b.mu.Lock()
		defer h.b.mu.Unlock()
		s := h.b.sessionLocked(id)
		s.running = true
		s.status = db.StatusBusy
		s.accepted++
	}); err != nil {
		h.t.Fatal(err)
	}
}

func TestRemarksDurableSettlement(t *testing.T) {
	h := newHarness(t, scripted)
	h.start("thr")
	restore := breakSpool(t, h)
	h.b.publishMessage("thr", map[string]any{"id": "msg_a", "sessionID": "thr", "role": "assistant"}, []map[string]any{{"id": "prt_a", "messageID": "msg_a", "sessionID": "thr", "type": "text", "text": "retained"}})
	h.b.settle("thr", db.StatusDone, 1)
	h.b.mu.Lock()
	running := h.b.sessions["thr"].running
	h.b.mu.Unlock()
	restore()
	if !running {
		t.Fatal("idle overtook failed upsert")
	}
	h.next("message.part.updated", func(p map[string]any) bool { return part(p)["text"] == "retained" })
	h.next("session.idle", nil)
	if events := h.b.snapshot("thr"); events[len(events)-1].Type != "session.idle" {
		t.Fatal(events)
	}
}

func TestRemarksPromptRecordingRetries(t *testing.T) {
	t.Setenv("FAKE_CODEX_APPROVAL", "1")
	h, _ := acpHarness(t)
	createACP(t, h)
	sendACP(t, h, platforms.SendMessageRequest{Message: "hi"})
	id := h.next("permission.asked", nil)["id"].(string)
	// ACP requests can overtake earlier notification callbacks. Drain those
	// callbacks before breaking storage, so only resolution writes fail here.
	barrier := acp.SessionNotification{SessionId: "thr", Update: acp.SessionUpdate{
		AvailableCommandsUpdate: &acp.SessionAvailableCommandsUpdate{AvailableCommands: []acp.AvailableCommand{}},
	}}
	if _, err := h.b.acp.client.CallExtension(t.Context(), "_test/notify", barrier); err != nil {
		t.Fatal(err)
	}
	restore := breakSpool(t, h)
	for range 2 {
		if err := h.b.replyPermission(id, "once"); !errors.Is(err, platforms.ErrReplyUnrecorded) {
			t.Error(err)
		}
	}
	if err := h.b.replyPermission(id, "reject"); err == nil {
		t.Fatal("changed delivered decision")
	}
	restore()
	if err := h.b.replyPermission(id, "once"); err != nil {
		t.Fatal(err)
	}
	h.next("session.idle", nil)
}

func TestRemarksOversizedUpsert(t *testing.T) {
	h := newHarness(t, scripted)
	h.start("thr")
	h.b.publishMessage("thr", nil, []map[string]any{{"id": "prt_a", "messageID": "msg_a", "sessionID": "thr", "type": "text", "text": strings.Repeat("x", 9<<20)}})
	h.b.settle("thr", db.StatusDone, 1)
	for _, event := range h.b.snapshot("thr") {
		if event.Type == "message.part.updated" {
			if !strings.Contains(string(event.Properties), "truncated") {
				t.Fatal("unbounded output")
			}
			return
		}
	}
	t.Fatal("lost oversized part")
}
