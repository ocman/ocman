package codextest

import (
	"encoding/json"
	"maps"
	"strconv"
	"sync"
	"time"

	codexappserver "github.com/NoUseFreak/ocman/internal/platforms/codex/appserver"
)

// World constants describe the store World serves.
const (
	StoredThread = "11111111-2222-4333-8444-555555555555"
	StoredReply  = "CODEX_STORED_REPLY"
	LiveReply    = "CODEX_LIVE_REPLY"
	Directory    = "/work/codex"
)

// World scripts a small Codex for end-to-end tests: one stored thread with
// a completed turn, new threads, and turns that stream LiveReply and
// complete. Each process is independent, as each real app-server reads the
// same store.
func World(s *Server) {
	var mu sync.Mutex
	next := 0
	thread := func(id string) map[string]any {
		return map[string]any{"id": id, "sessionId": id, "cwd": Directory, "source": "cli", "status": map[string]any{"type": "notLoaded"},
			"createdAt": 1790000000, "updatedAt": 1790000100, "modelProvider": "openai", "model": "gpt-test", "preview": "stored question", "turns": []any{}}
	}
	id := func(raw json.RawMessage) string {
		var p struct {
			ThreadID string `json:"threadId"`
		}
		_ = json.Unmarshal(raw, &p)
		return p.ThreadID
	}
	threadResp := func(t map[string]any) map[string]any {
		return map[string]any{"thread": t, "model": "gpt-test", "modelProvider": "openai", "cwd": t["cwd"]}
	}
	s.Handle(codexappserver.MethodThreadList, func(raw json.RawMessage) (any, error) {
		var p codexappserver.ThreadListParams
		_ = json.Unmarshal(raw, &p)
		if p.Archived != nil && *p.Archived {
			return map[string]any{"data": []any{}}, nil
		}
		return map[string]any{"data": []any{thread(StoredThread)}}, nil
	})
	s.Handle(codexappserver.MethodThreadRead, func(raw json.RawMessage) (any, error) {
		return map[string]any{"thread": thread(id(raw))}, nil
	})
	s.Handle(codexappserver.MethodThreadTurns, func(raw json.RawMessage) (any, error) {
		if id(raw) != StoredThread {
			return map[string]any{"data": []any{}}, nil
		}
		return map[string]any{"data": []any{map[string]any{"id": "turn-stored", "status": "completed", "items": []any{},
			"startedAt": 1790000000, "completedAt": 1790000010}}}, nil
	})
	s.Handle(codexappserver.MethodThreadItems, func(raw json.RawMessage) (any, error) {
		if id(raw) != StoredThread {
			return map[string]any{"data": []any{}}, nil
		}
		return map[string]any{"data": []any{
			map[string]any{"turnId": "turn-stored", "startedAtMs": 1790000000000, "completedAtMs": 1790000000000,
				"item": map[string]any{"type": "userMessage", "id": "item-user", "content": []any{codexappserver.TextInput("stored question")}}},
			map[string]any{"turnId": "turn-stored", "startedAtMs": 1790000001000, "completedAtMs": 1790000002000,
				"item": map[string]any{"type": "agentMessage", "id": "item-reply", "text": StoredReply}},
		}}, nil
	})
	s.Handle(codexappserver.MethodThreadStart, func(raw json.RawMessage) (any, error) {
		var p codexappserver.ThreadStartParams
		_ = json.Unmarshal(raw, &p)
		mu.Lock()
		next++
		t := thread("22222222-3333-4444-8555-" + strconv.FormatInt(int64(100000000000+next), 10))
		mu.Unlock()
		t["cwd"] = p.Cwd
		return threadResp(t), nil
	})
	s.Handle(codexappserver.MethodThreadResume, func(raw json.RawMessage) (any, error) { return threadResp(thread(id(raw))), nil })
	s.Handle(codexappserver.MethodThreadNameSet, func(json.RawMessage) (any, error) { return map[string]any{}, nil })
	s.Handle(codexappserver.MethodTurnInterrupt, func(json.RawMessage) (any, error) { return map[string]any{}, nil })
	s.Handle(codexappserver.MethodTurnStart, func(raw json.RawMessage) (any, error) {
		var p codexappserver.TurnStartParams
		_ = json.Unmarshal(raw, &p)
		mu.Lock()
		next++
		turn := "turn-" + strconv.Itoa(next)
		mu.Unlock()
		go playTurn(s, p, turn)
		return map[string]any{"turn": map[string]any{"id": turn, "status": "inProgress", "items": []any{}}}, nil
	})
}

func playTurn(s *Server, p codexappserver.TurnStartParams, turn string) {
	time.Sleep(20 * time.Millisecond)
	base := map[string]any{"threadId": p.ThreadID, "turnId": turn}
	with := func(extra map[string]any) map[string]any {
		out := maps.Clone(base)
		maps.Copy(out, extra)
		return out
	}
	now := time.Now().UnixMilli()
	_ = s.Notify(codexappserver.NotifyTurnStarted, map[string]any{"threadId": p.ThreadID, "turn": map[string]any{"id": turn, "status": "inProgress"}})
	user := map[string]any{"type": "userMessage", "id": turn + "-user", "content": p.Input}
	_ = s.Notify(codexappserver.NotifyItemStarted, with(map[string]any{"item": user, "startedAtMs": now}))
	_ = s.Notify(codexappserver.NotifyItemCompleted, with(map[string]any{"item": user, "completedAtMs": now}))
	reply := map[string]any{"type": "agentMessage", "id": turn + "-reply", "text": ""}
	_ = s.Notify(codexappserver.NotifyItemStarted, with(map[string]any{"item": reply, "startedAtMs": now + 1}))
	_ = s.Notify(codexappserver.NotifyAgentMessageDelta, with(map[string]any{"itemId": turn + "-reply", "delta": LiveReply}))
	reply["text"] = LiveReply
	_ = s.Notify(codexappserver.NotifyItemCompleted, with(map[string]any{"item": reply, "completedAtMs": now + 2}))
	_ = s.Notify(codexappserver.NotifyTurnCompleted, map[string]any{"threadId": p.ThreadID, "turn": map[string]any{"id": turn, "status": "completed"}})
}
