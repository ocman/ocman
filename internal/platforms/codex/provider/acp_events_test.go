package codexprovider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/ocman/ocman/internal/platforms"
	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
)

func TestACPToolAndThoughtUpdatesUseNativeConverter(t *testing.T) {
	h, _ := acpHarness(t)
	turn := &acpTurn{conv: &Converter{SessionID: "thr"}, items: map[string]*acpItem{}, started: 1000, userSeeded: true}
	h.b.acp.turns["thr"] = turn
	update := func(raw string) error {
		var event acp.SessionNotification
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			t.Fatal(err)
		}
		return h.b.acp.update(t.Context(), event)
	}
	for _, raw := range []string{
		`{"sessionId":"thr","update":{"sessionUpdate":"agent_thought_chunk","messageId":"reason","content":{"type":"text","text":"Think "}}}`,
		`{"sessionId":"thr","update":{"sessionUpdate":"agent_thought_chunk","messageId":"reason","content":{"type":"text","text":"more"}}}`,
		`{"sessionId":"thr","update":{"sessionUpdate":"tool_call","toolCallId":"command","title":"ls","kind":"execute","status":"in_progress","rawInput":{"command":"ls"}}}`,
		`{"sessionId":"thr","update":{"sessionUpdate":"tool_call_update","toolCallId":"command","title":"done","kind":"read","status":"completed","rawInput":{"command":"ls"},"rawOutput":"output","locations":[{"path":"/repo/x"}],"content":[{"type":"content","content":{"type":"text","text":"content output"}}]}}`,
		`{"sessionId":"thr","update":{"sessionUpdate":"tool_call","toolCallId":"generic","title":"MCP tool","kind":"other","status":"failed","rawInput":{"x":1},"content":[{"type":"content","content":{"type":"text","text":"failed"}}]}}`,
		`{"sessionId":"thr","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"notice"}}}`,
		`{"sessionId":"thr","update":{"sessionUpdate":"agent_message_chunk","messageId":"image","content":{"type":"image","mimeType":"image/png","data":"AA"}}}`,
		`{"sessionId":"thr","update":{"sessionUpdate":"plan","entries":[]}}`,
		`{"sessionId":"not-owned","update":{"sessionUpdate":"plan","entries":[]}}`,
	} {
		if err := update(raw); err != nil {
			t.Fatal(err)
		}
	}
	if turn.items["reason"].native.Summary[0] != "Think more" {
		t.Fatal(turn.items["reason"])
	}
	tool := turn.items["command"].native
	if tool.Type != codexappserver.ItemCommandExecution || tool.Status != "completed" || *tool.AggregatedOutput != "output" {
		t.Fatalf("%+v", tool)
	}
	if got := ItemPart(turn.items["generic"].native, 1)["state"].(map[string]any)["error"]; got != "failed" {
		t.Fatal(got)
	}
	// An update for a tool whose start was never seen is synthesized, not a
	// failure that would end the turn.
	if err := update(`{"sessionId":"thr","update":{"sessionUpdate":"tool_call_update","toolCallId":"unknown","title":"late","status":"completed","rawInput":{"command":"pwd"}}}`); err != nil {
		t.Fatal(err)
	}
	if late := turn.items["unknown"]; late == nil || late.native.Command != "pwd" || late.native.Status != "completed" {
		t.Fatalf("unknown tool update was not synthesized: %+v", late)
	}
}

// The SDK delivers every session's notifications on one goroutine. Waiting for
// one session's native user identity must never stall that shared stream.
func TestACPUpdateNeverWaitsForNativeUserIdentity(t *testing.T) {
	h, _ := acpHarness(t)
	if _, _, err := h.b.conn(t.Context()); err != nil {
		t.Fatal(err)
	}
	var materialized atomic.Bool
	h.fake().Handle(codexappserver.MethodThreadItems, func(json.RawMessage) (any, error) {
		if !materialized.Load() {
			return nil, &codexappserver.RPCError{Code: -32600, Message: "no rollout"}
		}
		return map[string]any{"data": []codexappserver.ItemEntry{
			{TurnID: "t1", StartedAtMs: 1000, Item: codexappserver.Item{ID: "u1", Type: "userMessage", Content: []codexappserver.UserInput{{Type: "text", Text: "hi"}}}},
			{TurnID: "t1", StartedAtMs: 1000, Item: codexappserver.Item{ID: "a1", Type: "agentMessage"}},
		}}, nil
	})
	turn := &acpTurn{conv: &Converter{SessionID: "thr"}, items: map[string]*acpItem{}, started: 1000}
	other := &acpTurn{conv: &Converter{SessionID: "other"}, items: map[string]*acpItem{}, started: 1000, userSeeded: true}
	h.b.acp.mu.Lock()
	h.b.acp.turns["thr"], h.b.acp.turns["other"] = turn, other
	h.b.acp.mu.Unlock()
	update := func(raw string) error {
		var event acp.SessionNotification
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			t.Fatal(err)
		}
		return h.b.acp.update(t.Context(), event)
	}
	start := time.Now()
	if err := update(`{"sessionId":"thr","update":{"sessionUpdate":"agent_message_chunk","messageId":"a1","content":{"type":"text","text":"Hel"}}}`); err != nil {
		t.Fatal(err)
	}
	if err := update(`{"sessionId":"other","update":{"sessionUpdate":"agent_message_chunk","messageId":"b1","content":{"type":"text","text":"unblocked"}}}`); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("session updates blocked on native history for %s", elapsed)
	}
	h.next("message.part.updated", func(p map[string]any) bool { return part(p)["text"] == "unblocked" })
	if err := update(`{"sessionId":"thr","update":{"sessionUpdate":"agent_message_chunk","messageId":"a1","content":{"type":"text","text":"lo"}}}`); err != nil {
		t.Fatal(err)
	}
	materialized.Store(true)
	user := h.next("message.updated", func(p map[string]any) bool { return info(p)["sessionID"] == "thr" })
	if info(user)["id"] != "msg_u1" || info(user)["role"] != "user" {
		t.Fatalf("buffered reply published before its native user: %+v", user)
	}
	h.next("message.part.updated", func(p map[string]any) bool { return part(p)["id"] == "prt_a1" && part(p)["text"] == "Hello" })
	turn.mu.Lock()
	seeded, native := turn.userSeeded, turn.nativeID
	turn.mu.Unlock()
	if !seeded || native != "t1" {
		t.Fatalf("seeded=%v native=%q", seeded, native)
	}
}

func TestACPUserSeedingFailureFailsOnlyItsTurn(t *testing.T) {
	h, _ := acpHarness(t)
	createACP(t, h)
	if _, _, err := h.b.conn(t.Context()); err != nil {
		t.Fatal(err)
	}
	h.fake().Handle(codexappserver.MethodThreadItems, func(json.RawMessage) (any, error) {
		return nil, errors.New("items unavailable")
	})
	sendACP(t, h, platforms.SendMessageRequest{Message: "hi"})
	failed := h.next("message.updated", func(p map[string]any) bool { return info(p)["error"] != nil })
	if msg := info(failed)["error"].(map[string]any)["data"].(map[string]any)["message"].(string); !strings.Contains(msg, "items unavailable") {
		t.Fatal(msg)
	}
	h.next("session.idle", nil)
	h.b.acp.mu.Lock()
	c := h.b.acp.client
	h.b.acp.mu.Unlock()
	select {
	case <-c.Done():
		t.Fatal("one session's seeding failure disconnected the shared adapter")
	default:
	}
}

func TestACPReconciliationValidatesNativeSettlement(t *testing.T) {
	for _, tt := range []struct {
		name                          string
		turns                         []codexappserver.Turn
		base, native                  string
		cancelled, command, wantError bool
	}{
		{name: "missing", wantError: true},
		{name: "cancelled before insertion", cancelled: true},
		{name: "no new turn", turns: []codexappserver.Turn{{ID: "old", Status: "completed"}}, base: "old", wantError: true},
		{name: "local command", turns: []codexappserver.Turn{{ID: "old", Status: "completed"}}, base: "old", command: true},
		{name: "identity changed", turns: []codexappserver.Turn{{ID: "new", Status: "completed"}}, native: "different", wantError: true},
		{name: "not yet settled", turns: []codexappserver.Turn{{ID: "new", Status: "inProgress"}}, wantError: true},
		{name: "failed", turns: []codexappserver.Turn{{ID: "new", Status: "failed"}}},
		{name: "interrupted", turns: []codexappserver.Turn{{ID: "new", Status: "interrupted", CompletedAt: 1}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h, _ := acpHarness(t)
			_, _, err := h.b.conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			h.fake().Handle(codexappserver.MethodThreadTurns, func(json.RawMessage) (any, error) { return map[string]any{"data": tt.turns}, nil })
			turn := &acpTurn{conv: &Converter{SessionID: "thr"}, baseTurn: tt.base, nativeID: tt.native, command: tt.command}
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			err = h.b.acp.reconcile(ctx, "thr", turn, tt.cancelled)
			if (err != nil) != tt.wantError {
				t.Fatalf("%v", err)
			}
			if tt.name == "failed" && !turn.failed {
				t.Fatal("failed native turn reported success")
			}
			if tt.name == "interrupted" && !turn.cancelled {
				t.Fatal("interrupted native turn reported success")
			}
		})
	}
}

func TestACPControlRoutes(t *testing.T) {
	h, _ := acpHarness(t)
	createACP(t, h)
	var fork platforms.CreateSessionResponse
	if code := h.call("POST", "/sessions/thr/fork", platforms.ForkSessionRequest{}, &fork); code != http.StatusOK || fork.ID != "thr-fork" {
		t.Fatalf("%d %+v", code, fork)
	}
	if code := h.call("POST", "/sessions/thr/rename", platforms.RenameSessionRequest{Title: "Renamed"}, nil); code != http.StatusNoContent {
		t.Fatal(code)
	}
	if code := h.call("POST", "/sessions/thr/rename", platforms.RenameSessionRequest{}, nil); code != http.StatusBadRequest {
		t.Fatal(code)
	}
	if code := h.call("POST", "/sessions/thr/abort", nil, nil); code != http.StatusNoContent {
		t.Fatal(code)
	}
	if code := h.call("GET", "/sessions/thr/ownership", nil, nil); code != http.StatusNotImplemented {
		t.Fatal(code)
	}
	if code := h.call("POST", "/unknown", nil, nil); code != http.StatusNotImplemented {
		t.Fatal(code)
	}
	if code := h.call("POST", "/sessions/thr/compact", nil, nil); code != http.StatusNoContent {
		t.Fatal(code)
	}
	h.next("session.idle", nil)
	if code := h.call("POST", "/sessions/thr/command", platforms.ExecuteCommandRequest{Command: "review", Arguments: "check this"}, nil); code != http.StatusNoContent {
		t.Fatal(code)
	}
	h.next("session.idle", nil)
}

func TestACPFinishInteractionsBeforeIdle(t *testing.T) {
	h, _ := acpHarness(t)
	pr := &prompt{session: "thr", recorded: make(chan struct{}), deliver: func(any) error { return nil }}
	h.b.mu.Lock()
	h.b.prompts["pending"] = pr
	h.b.mu.Unlock()
	if !h.b.acp.finishInteractions("thr") {
		t.Fatal("failed to persist cancellation")
	}
	select {
	case <-pr.recorded:
	default:
		t.Fatal("resolution not recorded")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	b := &backend{ctx: ctx, sup: h.b.sup, prompts: map[string]*prompt{"pending": {session: "thr", recorded: make(chan struct{}), deliver: func(any) error { return nil }}}}
	if newACPExecution(b).finishInteractions("thr") {
		t.Fatal("supervisor shutdown was ignored")
	}
}

func TestACPReaderErrorsDoNotBecomeExecution(t *testing.T) {
	h, _ := acpHarness(t)
	_, _, err := h.b.conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	h.fake().Handle(codexappserver.MethodThreadRead, func(json.RawMessage) (any, error) {
		return &codexappserver.ThreadReadResponse{Thread: codexappserver.Thread{ID: "other"}}, nil
	})
	c, err := h.b.acp.conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := h.b.acp.resume(t.Context(), c, "thr"); err == nil {
		t.Fatal("accepted another thread's metadata")
	}
	h.fake().Handle(codexappserver.MethodThreadRead, func(json.RawMessage) (any, error) { return nil, errors.New("unreadable") })
	if err := h.b.acp.resume(t.Context(), c, "thr"); err == nil {
		t.Fatal("reader failure ignored")
	}
}
