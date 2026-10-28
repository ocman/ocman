package codexprovider

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

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
	if err := os.WriteFile(h.spool, nil, 0600); err != nil {
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

func TestRemarksPagination(t *testing.T) {
	h := newHarness(t, func(s *codextest.Server) {
		scripted(s)
		for _, f := range []struct{ method, file string }{
			{codexappserver.MethodThreadRead, "thread-read.json"},
			{codexappserver.MethodThreadTurns, "turns.json"},
			{codexappserver.MethodThreadItems, "items.json"},
		} {
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

func TestRemarksConcurrentSend(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	h := newHarness(t, func(s *codextest.Server) {
		scripted(s)
		s.Handle(codexappserver.MethodThreadResume, func(json.RawMessage) (any, error) {
			once.Do(func() { close(entered) })
			<-release
			return map[string]any{"thread": thread("thr"), "cwd": "/repo"}, nil
		})
	})
	done := make(chan error, 1)
	go func() { done <- h.b.send(t.Context(), "thr", platforms.SendMessageRequest{Message: "one"}) }()
	<-entered
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	err := h.b.send(ctx, "thr", platforms.SendMessageRequest{Message: "two"})
	cancel()
	close(release)
	if first := <-done; first != nil {
		t.Fatal(first)
	}
	if !errors.Is(err, platforms.ErrBusy) {
		t.Fatalf("competing send = %v", err)
	}
}

func TestRemarksUncertainStart(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	h := newHarness(t, func(s *codextest.Server) {
		scripted(s)
		s.Handle(codexappserver.MethodTurnStart, func(json.RawMessage) (any, error) {
			close(entered)
			<-release
			return map[string]any{"turn": map[string]any{"id": "t1"}}, nil
		})
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- h.b.send(ctx, "thr", platforms.SendMessageRequest{Message: "one"}) }()
	<-entered
	cancel()
	<-done
	close(release)
	h.b.mu.Lock()
	running := h.b.sessions["thr"].running
	h.b.mu.Unlock()
	if !running {
		t.Fatal("uncertain acceptance published premature idle")
	}
	if err := h.b.send(t.Context(), "thr", platforms.SendMessageRequest{Message: "two"}); !errors.Is(err, platforms.ErrBusy) {
		t.Fatalf("send = %v", err)
	}
	notify(t, h.fake(), codexappserver.NotifyTurnStarted, map[string]any{"threadId": "thr", "turn": map[string]any{"id": "t1"}})
	notify(t, h.fake(), codexappserver.NotifyItemStarted, map[string]any{"threadId": "thr", "item": map[string]any{"id": "after-cancel", "type": "agentMessage", "text": "still running"}})
	h.next("message.part.updated", func(p map[string]any) bool { return part(p)["text"] == "still running" })
	if err := h.b.abort(t.Context(), "thr"); err != nil {
		t.Fatalf("abort after uncertain start = %v", err)
	}
	notify(t, h.fake(), codexappserver.NotifyTurnCompleted, map[string]any{"threadId": "thr", "turn": map[string]any{"id": "t1", "status": "completed"}})
	h.next("session.idle", nil)
}

func TestRemarksDurableSettlement(t *testing.T) {
	h := newHarness(t, scripted)
	h.start("thr")
	restore := breakSpool(t, h)
	h.b.settle("thr", db.StatusDone, 1)
	h.b.mu.Lock()
	running := h.b.sessions["thr"].running
	h.b.mu.Unlock()
	restore()
	if !running {
		t.Fatal("failed publication discarded running turn")
	}
	h.next("session.idle", nil)
}

func TestRemarksPromptRecordingRetries(t *testing.T) {
	h := newHarness(t, scripted)
	h.start("thr")
	response, err := h.fake().Request(codexappserver.RequestCommandApproval, map[string]any{"threadId": "thr", "command": "ls"})
	if err != nil {
		t.Fatal(err)
	}
	id := h.next("permission.asked", nil)["id"].(string)
	restore := breakSpool(t, h)
	for range 2 {
		if err := h.b.replyPermission(id, "once"); !errors.Is(err, platforms.ErrReplyUnrecorded) {
			t.Errorf("reply = %v", err)
		}
	}
	<-response
	restore()
	if err := h.b.replyPermission(id, "once"); err != nil {
		t.Fatal(err)
	}
}

func TestRemarksLiveModel(t *testing.T) {
	h := newHarness(t, scripted)
	h.start("thr")
	notify(t, h.fake(), codexappserver.NotifyItemStarted, map[string]any{"threadId": "thr", "item": map[string]any{"id": "a", "type": "agentMessage", "text": "hi"}})
	i := info(h.next("message.updated", nil))
	if i["providerID"] != "openai" || i["modelID"] != "gpt" {
		t.Fatalf("attribution = %v", i)
	}
}

func TestRemarksOversizedUpsert(t *testing.T) {
	h := newHarness(t, scripted)
	h.start("thr")
	h.b.item("thr", codexappserver.Item{ID: "a", Type: "agentMessage", Text: strings.Repeat("x", 9<<20)})
	h.b.turnCompleted("thr", &codexappserver.Turn{ID: "t1", Status: codexappserver.TurnCompleted})
	events := h.b.snapshot("thr")
	found := false
	for _, e := range events {
		if e.Type == "message.part.updated" {
			found = true
			if !strings.Contains(string(e.Properties), "truncated") {
				t.Fatal("oversized output not explicitly bounded")
			}
		}
	}
	if !found {
		t.Fatal("successful completion lost oversized part")
	}
}
