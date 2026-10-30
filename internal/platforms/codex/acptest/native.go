package acptest

import (
	"encoding/json"

	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
	"github.com/ocman/ocman/internal/platforms/codex/appserver/codextest"
)

// ConfigureNative exposes only metadata and stored history. In particular it
// never handles thread/start, thread/resume or turn/start for the ACP executor.
func ConfigureNative(server *codextest.Server, path string, withStored ...bool) {
	stored := len(withStored) > 0 && withStored[0]
	threadID := func(raw json.RawMessage) string {
		var req struct {
			ID string `json:"threadId"`
		}
		_ = json.Unmarshal(raw, &req)
		return req.ID
	}
	thread := func(id string) codexappserver.Thread {
		state := ReadState(path)
		dir := state.Directory
		if dir == "" {
			dir = "/repo"
		}
		created, updated := int64(1), int64(2)
		if state.CreatedAt > 0 {
			created = state.CreatedAt
			updated = created + state.Revision
		}
		if id == codextest.StoredThread {
			dir = codextest.Directory
			created = 1790000000
			updated = 1790000100
		}
		return codexappserver.Thread{ID: id, SessionID: id, Cwd: dir, ModelProvider: "openai", Model: "gpt-test", Preview: "hello", CreatedAt: created, UpdatedAt: updated,
			Source: codexappserver.SessionSource{Kind: "appServer"}, Status: codexappserver.ThreadStatus{Type: "notLoaded"}}
	}
	if stored {
		server.Handle(codexappserver.MethodThreadList, func(raw json.RawMessage) (any, error) {
			var request codexappserver.ThreadListParams
			_ = json.Unmarshal(raw, &request)
			if request.Archived != nil && *request.Archived {
				return map[string]any{"data": []any{}}, nil
			}
			threads := []codexappserver.Thread{thread(codextest.StoredThread)}
			if ReadState(path).Directory != "" {
				threads = append(threads, thread("thr"))
			}
			return map[string]any{"data": threads}, nil
		})
	}
	server.Handle(codexappserver.MethodModelList, func(json.RawMessage) (any, error) {
		return map[string]any{"data": []any{map[string]any{"id": "m", "model": "gpt-test", "isDefault": true}}}, nil
	})
	server.Handle(codexappserver.MethodThreadRead, func(raw json.RawMessage) (any, error) {
		return map[string]any{"thread": thread(threadID(raw))}, nil
	})
	server.Handle(codexappserver.MethodThreadTurns, func(raw json.RawMessage) (any, error) {
		if stored && threadID(raw) == codextest.StoredThread {
			return map[string]any{"data": []codexappserver.Turn{{ID: "turn-stored", Status: "completed", StartedAt: 1790000000, CompletedAt: 1790000010}}}, nil
		}
		return map[string]any{"data": ReadState(path).Turns}, nil
	})
	server.Handle(codexappserver.MethodThreadItems, func(raw json.RawMessage) (any, error) {
		if stored && threadID(raw) == codextest.StoredThread {
			return map[string]any{"data": []codexappserver.ItemEntry{
				{TurnID: "turn-stored", StartedAtMs: 1790000000000, CompletedAtMs: 1790000000000, Item: codexappserver.Item{ID: "item-user", Type: "userMessage", Content: []codexappserver.UserInput{codexappserver.TextInput("stored question")}}},
				{TurnID: "turn-stored", StartedAtMs: 1790000001000, CompletedAtMs: 1790000002000, Item: codexappserver.Item{ID: "item-reply", Type: "agentMessage", Text: codextest.StoredReply}},
			}}, nil
		}
		return map[string]any{"data": ReadState(path).Items}, nil
	})
}
