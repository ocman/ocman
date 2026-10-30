package codexprovider

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
	"github.com/ocman/ocman/internal/platforms/codex/appserver/codextest"
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

func TestRemarksPagination(t *testing.T) {
	h := newHarness(t, func(s *codextest.Server) {
		for _, f := range []struct{ method, file string }{{codexappserver.MethodThreadRead, "thread-read.json"}, {codexappserver.MethodThreadTurns, "turns.json"}, {codexappserver.MethodThreadItems, "items.json"}} {
			data, err := os.ReadFile("testdata/0.164.0/" + f.file)
			if err != nil {
				t.Fatal(err)
			}
			s.Handle(f.method, func(json.RawMessage) (any, error) { return json.RawMessage(data), nil })
		}
	})
	for _, p := range [][3]int{{2, -10, 2}, {math.MaxInt, math.MaxInt, 0}, {math.MaxInt, 0, 4}, {0, 0, 4}, {2, 1, 2}, {2, 4, 0}} {
		detail, err := h.b.detail(t.Context(), "thr", p[0], p[1])
		if err != nil || len(detail.Messages) != p[2] {
			t.Fatalf("page %v: %v %v", p, detail, err)
		}
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
