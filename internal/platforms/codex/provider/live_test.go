package codexprovider

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
	"github.com/ocman/ocman/internal/platforms/codex/appserver/codextest"
)

func thread(id string) map[string]any {
	return map[string]any{"id": id, "cwd": "/repo", "source": "appServer", "status": map[string]any{"type": "idle"},
		"createdAt": 1, "updatedAt": 2, "modelProvider": "openai", "preview": "hello", "turns": []any{}}
}

// scripted answers thread and turn requests; tests push notifications.
func scripted(s *codextest.Server) {
	resp := func(json.RawMessage) (any, error) {
		return map[string]any{"thread": thread("thr"), "model": "gpt", "modelProvider": "openai", "cwd": "/repo"}, nil
	}
	s.Handle(codexappserver.MethodThreadStart, resp)
	s.Handle(codexappserver.MethodThreadResume, resp)
	s.Handle(codexappserver.MethodTurnStart, func(json.RawMessage) (any, error) {
		return map[string]any{"turn": map[string]any{"id": "t1", "status": "inProgress", "items": []any{}}}, nil
	})
	empty := func(json.RawMessage) (any, error) { return map[string]any{}, nil }
	s.Handle(codexappserver.MethodThreadNameSet, empty)
	s.Handle(codexappserver.MethodTurnInterrupt, empty)
	s.Handle(codexappserver.MethodThreadList, func(json.RawMessage) (any, error) {
		active := thread("thr")
		active["status"] = map[string]any{"type": "active"}
		return map[string]any{"data": []any{thread("old"), active, map[string]any{"id": "eph", "ephemeral": true, "source": "cli", "status": map[string]any{"type": "idle"}}}}, nil
	})
	s.Handle(codexappserver.MethodThreadRead, func(json.RawMessage) (any, error) {
		return nil, &codexappserver.RPCError{Code: -32600, Message: "thread not loaded: x"}
	})
}

func (h *harness) start(id string) {
	h.t.Helper()
	if code := h.call(http.MethodPost, "/sessions/"+id+"/send", platforms.SendMessageRequest{Message: "hi"}, nil); code != 204 {
		h.t.Fatalf("send = %d", code)
	}
	h.next("session.status", func(p map[string]any) bool { return p["status"].(map[string]any)["type"] == "busy" })
}

func notify(t *testing.T, s *codextest.Server, method string, params any) {
	t.Helper()
	if err := s.Notify(method, params); err != nil {
		t.Fatal(err)
	}
}

func TestStreamingDeltas(t *testing.T) {
	h := newHarness(t, scripted)
	h.start("thr")
	s := h.fake()
	one := 1
	notify(t, s, codexappserver.NotifyTurnStarted, map[string]any{"threadId": "thr", "turn": map[string]any{"id": "t1", "status": "inProgress"}})
	notify(t, s, codexappserver.NotifyItemStarted, map[string]any{"threadId": "thr", "turnId": "t1", "startedAtMs": 10,
		"item": map[string]any{"type": "reasoning", "id": "r", "summary": []any{}, "content": []any{}}})
	notify(t, s, codexappserver.NotifyReasoningSummary, map[string]any{"threadId": "thr", "itemId": "r", "delta": "a", "summaryIndex": 0})
	notify(t, s, codexappserver.NotifyReasoningSummary, map[string]any{"threadId": "thr", "itemId": "r", "delta": "b", "summaryIndex": one})
	got := h.next("message.part.updated", func(p map[string]any) bool { return part(p)["text"] == "a\n\nb" })
	if part(got)["id"] != "prt_r" {
		t.Fatalf("reasoning part = %v", got)
	}
	notify(t, s, codexappserver.NotifyItemStarted, map[string]any{"threadId": "thr", "turnId": "t1",
		"item": map[string]any{"type": "commandExecution", "id": "c", "command": "/bin/zsh -lc ls", "status": "inProgress"}})
	notify(t, s, codexappserver.NotifyCommandOutputDelta, map[string]any{"threadId": "thr", "itemId": "c", "delta": "a.go\n"})
	h.next("message.part.updated", func(p map[string]any) bool {
		st, _ := part(p)["state"].(map[string]any)
		return st != nil && st["output"] == "a.go\n"
	})
	notify(t, s, codexappserver.NotifyItemStarted, map[string]any{"threadId": "thr", "turnId": "t1",
		"item": map[string]any{"type": "agentMessage", "id": "a", "text": ""}})
	notify(t, s, codexappserver.NotifyAgentMessageDelta, map[string]any{"threadId": "thr", "itemId": "a", "delta": "Do"})
	notify(t, s, codexappserver.NotifyAgentMessageDelta, map[string]any{"threadId": "thr", "itemId": "a", "delta": "ne"})
	h.next("message.part.updated", func(p map[string]any) bool { return part(p)["text"] == "Done" })
	// Deltas for unknown items and threads are dropped.
	notify(t, s, codexappserver.NotifyAgentMessageDelta, map[string]any{"threadId": "thr", "itemId": "nope", "delta": "x"})
	notify(t, s, codexappserver.NotifyAgentMessageDelta, map[string]any{"threadId": "other", "itemId": "a", "delta": "x"})
	notify(t, s, codexappserver.NotifyThreadNameUpdated, map[string]any{"threadId": "thr", "threadName": "Named"})
	// While the turn runs, the reply is open and the session is busy.
	events := h.b.snapshot("thr")
	if len(events) == 0 || events[len(events)-1].Type != "session.status" {
		t.Fatalf("running snapshot = %+v", events)
	}
	notify(t, s, codexappserver.NotifyTurnCompleted, map[string]any{"threadId": "thr", "turn": map[string]any{"id": "t1", "status": "completed"}})
	h.next("session.idle", nil)
	events = h.b.snapshot("thr")
	if events[len(events)-1].Type != "session.idle" {
		t.Fatalf("settled snapshot = %+v", events)
	}
	var runtime []db.Session
	h.call(http.MethodGet, "/sessions?runtime=1&directory=/repo", nil, &runtime)
	if len(runtime) != 1 || runtime[0].Title != "Named" || runtime[0].Status != db.StatusDone {
		t.Fatalf("runtime = %+v", runtime)
	}
	if empty := h.b.snapshot("unknown"); len(empty) != 0 {
		t.Fatalf("unknown snapshot = %+v", empty)
	}
}

func TestAppServerExitFailsTurn(t *testing.T) {
	h := newHarness(t, scripted)
	h.start("thr")
	s := h.fake()
	if _, err := s.Request(codexappserver.RequestCommandApproval, map[string]any{"threadId": "thr", "turnId": "t1", "itemId": "c", "command": "rm x"}); err != nil {
		t.Fatal(err)
	}
	asked := h.next("permission.asked", nil)
	h.b.closeClient()
	if r := h.next("permission.replied", nil); r["requestID"] != asked["id"] || r["reason"] != "cancelled" {
		t.Fatalf("cancellation = %v", r)
	}
	if e := h.next("message.updated", nil); info(e)["error"] == nil {
		t.Fatalf("error message = %v", e)
	}
	h.next("session.status", func(p map[string]any) bool { return p["status"].(map[string]any)["type"] == "error" })
	h.next("session.idle", nil)
	// The next send starts a new app-server and resumes the thread there.
	h.start("thr")
	if h.fake() == s || !slices.ContainsFunc(h.fake().Calls(), func(c codextest.Call) bool { return c.Method == codexappserver.MethodThreadResume }) {
		t.Fatal("thread was not resumed on a new app-server")
	}
}

func TestPromptWithdrawnAndQuestions(t *testing.T) {
	h := newHarness(t, scripted)
	h.start("thr")
	s := h.fake()
	if _, err := s.RequestID(json.RawMessage(`7`), codexappserver.RequestFileChangeApproval, map[string]any{"threadId": "thr", "itemId": "unknown"}); err != nil {
		t.Fatal(err)
	}
	asked := h.next("permission.asked", nil)
	if asked["permission"] != "edit" || asked["patterns"].([]any)[0] != "*" {
		t.Fatalf("edit without a known item = %v", asked)
	}
	notify(t, s, codexappserver.NotifyServerRequestDone, map[string]any{"threadId": "thr", "requestId": 7})
	if r := h.next("permission.replied", nil); r["reason"] != "cancelled" {
		t.Fatalf("withdrawal = %v", r)
	}
	questions := []map[string]any{{"id": "q1", "header": "H", "question": "Pick", "isOther": false, "isSecret": false,
		"options": []any{map[string]any{"label": "A", "description": "a"}}}, {"id": "q2", "question": "Free", "isOther": true}}
	reply, err := s.Request(codexappserver.RequestUserInput, map[string]any{"threadId": "thr", "itemId": "i", "questions": questions})
	if err != nil {
		t.Fatal(err)
	}
	q := h.next("question.asked", nil)
	qs := q["questions"].([]any)
	if len(qs) != 2 || qs[0].(map[string]any)["custom"] != false || qs[1].(map[string]any)["custom"] != true {
		t.Fatalf("questions = %v", q)
	}
	var pending []map[string]any
	h.call(http.MethodGet, "/questions?sessionID=thr", nil, &pending)
	if len(pending) != 1 {
		t.Fatalf("pending questions = %v", pending)
	}
	if code := h.call(http.MethodPost, "/permissions/"+q["id"].(string)+"/reply", platforms.RespondPermissionRequest{Reply: "once"}, nil); code != 404 {
		t.Fatalf("a question answered as a permission = %d", code)
	}
	h.call(http.MethodPost, "/questions/"+q["id"].(string)+"/reply", platforms.RespondQuestionRequest{Answers: [][]string{{"A"}, {"mine"}}}, nil)
	if got := <-reply; string(got.Result) != `{"answers":{"q1":{"answers":["A"]},"q2":{"answers":["mine"]}}}` {
		t.Fatalf("answers = %s", got.Result)
	}
	h.next("question.replied", nil)
	reply, _ = s.Request(codexappserver.RequestUserInput, map[string]any{"threadId": "thr", "itemId": "i", "questions": questions})
	q = h.next("question.asked", nil)
	h.call(http.MethodPost, "/questions/"+q["id"].(string)+"/reject", nil, nil)
	if got := <-reply; string(got.Result) != `{"answers":{}}` {
		t.Fatalf("rejection = %s", got.Result)
	}
	h.next("question.rejected", nil)
	// Requests ocman does not handle are refused rather than left waiting.
	reply, _ = s.Request("item/permissions/requestApproval", map[string]any{"threadId": "thr"})
	if got := <-reply; got.Error == nil || got.Error.Code != -32601 {
		t.Fatalf("unhandled request reply = %+v", got)
	}
	for _, body := range []any{platforms.RespondPermissionRequest{Reply: "maybe"}} {
		if code := h.call(http.MethodPost, "/permissions/x/reply", body, nil); code != 400 {
			t.Fatalf("invalid reply = %d", code)
		}
	}
	if code := h.call(http.MethodPost, "/questions/x/unknown", nil, nil); code != 501 {
		t.Fatalf("unknown prompt action = %d", code)
	}
}

func TestSendErrors(t *testing.T) {
	h := newHarness(t, scripted)
	if code := h.call(http.MethodPost, "/sessions/thr/send", platforms.SendMessageRequest{Message: "  "}, nil); code != 400 {
		t.Fatalf("empty send = %d", code)
	}
	if code := h.call(http.MethodPost, "/sessions/thr/send", "not an object", nil); code != 400 {
		t.Fatalf("bad body = %d", code)
	}
	h.start("thr")
	// A send while the turn runs steers it rather than starting another.
	h.fake().Handle(codexappserver.MethodTurnSteer, func(json.RawMessage) (any, error) { return map[string]any{"turnId": "t1"}, nil })
	if code := h.call(http.MethodPost, "/sessions/thr/send", platforms.SendMessageRequest{Message: "again"}, nil); code != 204 {
		t.Fatalf("busy send = %d", code)
	}
	if !slices.ContainsFunc(h.fake().Calls(), func(c codextest.Call) bool {
		return c.Method == codexappserver.MethodTurnSteer && strings.Contains(string(c.Params), `"expectedTurnId":"t1"`) && strings.Contains(string(c.Params), `"again"`)
	}) {
		t.Fatal("busy send did not steer the running turn")
	}
	h.fake().Handle(codexappserver.MethodTurnSteer, func(json.RawMessage) (any, error) {
		return nil, &codexappserver.RPCError{Code: -32600, Message: "turn is not steerable"}
	})
	if code := h.call(http.MethodPost, "/sessions/thr/send", platforms.SendMessageRequest{Message: "again"}, nil); code != 409 {
		t.Fatalf("unsteerable send = %d", code)
	}
	// Abort before turn/started is retried by the caller; after it, interrupts.
	if code := h.call(http.MethodPost, "/sessions/thr/abort", nil, nil); code != 204 {
		t.Fatalf("abort with the turn/start response's id = %d", code)
	}
	if !slices.ContainsFunc(h.fake().Calls(), func(c codextest.Call) bool {
		return c.Method == codexappserver.MethodTurnInterrupt && string(c.Params) == `{"threadId":"thr","turnId":"t1"}`
	}) {
		t.Fatal("turn was not interrupted")
	}
	notify(t, h.fake(), codexappserver.NotifyTurnCompleted, map[string]any{"threadId": "thr", "turn": map[string]any{"id": "t1", "status": "failed", "error": map[string]any{"message": "quota"}}})
	// The failure is the reply stored history gives the turn: msg_<turn id>.
	if e := h.next("message.updated", nil); info(e)["id"] != "msg_t1" || info(e)["error"].(map[string]any)["data"].(map[string]any)["message"] != "quota" {
		t.Fatalf("failure message = %v", e)
	}
	h.next("session.idle", nil)
	if code := h.call(http.MethodPost, "/sessions/thr/abort", nil, nil); code != 204 {
		t.Fatalf("abort while idle = %d", code)
	}

	h.fake().Handle(codexappserver.MethodTurnStart, func(json.RawMessage) (any, error) { return nil, errors.New("refused") })
	if code := h.call(http.MethodPost, "/sessions/thr/send", platforms.SendMessageRequest{Message: "x"}, nil); code != 500 {
		t.Fatalf("refused turn = %d", code)
	}
	h.next("session.status", func(p map[string]any) bool { return p["status"].(map[string]any)["type"] == "error" })
	h.next("session.idle", nil)

	h.fake().Handle(codexappserver.MethodThreadResume, func(json.RawMessage) (any, error) {
		return nil, &codexappserver.RPCError{Code: -32600, Message: "thread not loaded: gone"}
	})
	if code := h.call(http.MethodPost, "/sessions/gone/send", platforms.SendMessageRequest{Message: "x"}, nil); code != 404 {
		t.Fatalf("missing thread = %d", code)
	}
	h.mu.Lock()
	h.fail = errors.New("no codex")
	h.mu.Unlock()
	h.b.closeClient()
	time.Sleep(50 * time.Millisecond)
	if code := h.call(http.MethodPost, "/sessions/thr/send", platforms.SendMessageRequest{Message: "x"}, nil); code != 500 {
		t.Fatalf("send without an app-server = %d", code)
	}
}

func TestAbortBeforeTurnID(t *testing.T) {
	h := newHarness(t, func(s *codextest.Server) {
		scripted(s)
		s.Handle(codexappserver.MethodTurnStart, func(json.RawMessage) (any, error) {
			return map[string]any{"turn": map[string]any{"status": "inProgress"}}, nil
		})
	})
	h.start("thr")
	if code := h.call(http.MethodPost, "/sessions/thr/abort", nil, nil); code != 409 {
		t.Fatalf("abort before the turn id = %d", code)
	}
	if code := h.call(http.MethodPost, "/sessions/thr/send", platforms.SendMessageRequest{Message: "more"}, nil); code != 409 {
		t.Fatalf("send before the turn id = %d", code)
	}
}

func TestCreateRenameAndReads(t *testing.T) {
	h := newHarness(t, scripted)
	var resp platforms.CreateSessionResponse
	var perr struct{ Code string }
	if code := h.call(http.MethodPost, "/sessions", platforms.CreateSessionRequest{Directory: "relative"}, nil); code != 400 {
		t.Fatalf("relative create = %d", code)
	}
	h.call(http.MethodPost, "/sessions", platforms.CreateSessionRequest{Directory: "relative"}, &perr)
	if code := h.call(http.MethodPost, "/sessions", platforms.CreateSessionRequest{Directory: "/repo", Title: "T"}, &resp); code != 200 || resp.ID != "thr" {
		t.Fatalf("create = %d %+v", code, resp)
	}
	if !slices.ContainsFunc(h.fake().Calls(), func(c codextest.Call) bool { return c.Method == codexappserver.MethodThreadNameSet }) {
		t.Fatal("title was not set")
	}
	// A new thread with no stored history reads as an empty session.
	var detail platforms.SessionDetail
	if code := h.call(http.MethodGet, "/sessions/thr", nil, &detail); code != 200 || detail.Session.Title != "T" || len(detail.Messages) != 0 {
		t.Fatalf("new thread detail = %d %+v", code, detail)
	}
	if code := h.call(http.MethodGet, "/sessions/unknown", nil, nil); code != 404 {
		t.Fatalf("unknown detail = %d", code)
	}
	if code := h.call(http.MethodGet, "/sessions/unknown/lifecycle", nil, nil); code != 404 {
		t.Fatalf("unknown lifecycle = %d", code)
	}
	var life platforms.SessionLifecycle
	if code := h.call(http.MethodGet, "/sessions/thr/lifecycle", nil, &life); code != 200 || life.Status != db.StatusDone {
		t.Fatalf("lifecycle = %d %+v", code, life)
	}
	var list []db.Session
	h.call(http.MethodGet, "/sessions?directory=/repo&since=1000", nil, &list)
	if len(list) != 2 || list[1].Status != db.StatusInterrupted || !list[0].LiveConnection {
		t.Fatalf("list = %+v", list)
	}
	h.call(http.MethodGet, "/sessions?since=3000", nil, &list)
	if len(list) != 0 {
		t.Fatalf("since filter = %+v", list)
	}
	if code := h.call(http.MethodPost, "/sessions/thr/rename", platforms.RenameSessionRequest{Title: " "}, nil); code != 400 {
		t.Fatalf("empty rename = %d", code)
	}
	if code := h.call(http.MethodPost, "/sessions/thr/rename", platforms.RenameSessionRequest{Title: "New"}, nil); code != 204 {
		t.Fatalf("rename = %d", code)
	}
	for _, path := range []string{"/sessions/thr/revert", "/nope", "/sessions/x"} {
		if code := h.call(http.MethodPost, path, nil, nil); code != 501 {
			t.Errorf("POST %s = %d", path, code)
		}
	}
	h.fake().Handle(codexappserver.MethodModelList, func(json.RawMessage) (any, error) {
		return map[string]any{"data": []any{
			map[string]any{"id": "a", "model": "gpt-a", "displayName": "GPT A", "isDefault": true, "supportedReasoningEfforts": []any{map[string]any{"reasoningEffort": "high"}}},
			map[string]any{"id": "b", "model": "gpt-hidden", "hidden": true},
			map[string]any{"id": "c", "model": "gpt-c"},
		}}, nil
	})
	h.fake().Handle(codexappserver.MethodSkillsList, func(json.RawMessage) (any, error) {
		return map[string]any{"data": []any{map[string]any{"cwd": "/repo", "skills": []any{
			map[string]any{"name": "pdf", "description": "long", "shortDescription": "PDF tools", "path": "/s/pdf", "enabled": true},
			map[string]any{"name": "off", "description": "disabled", "path": "/s/off", "enabled": false},
			map[string]any{"name": "review", "description": "shadows a built-in", "path": "/s/r", "enabled": true}}}}}, nil
	})
	var catalog platforms.DirectoryCatalog
	if code := h.call(http.MethodGet, "/catalog?directory=/repo", nil, &catalog); code != 200 {
		t.Fatalf("catalog = %d", code)
	}
	var names []string
	for _, c := range catalog.Commands {
		names = append(names, c.Name+":"+c.Source+":"+c.Description)
	}
	if !slices.Equal(names, []string{"review:command:" + builtinCommands[0].Description, "compact:command:" + builtinCommands[1].Description, "pdf:skill:PDF tools"}) {
		t.Fatalf("commands = %q", names)
	}
	m := catalog.Models
	if len(m.Models) != 2 || m.Models[0].Model != "gpt-a" || m.Models[0].ModelName != "GPT A" || m.Models[0].Reasoning[0] != "high" ||
		!m.Models[0].IsProviderDefault || m.Models[1].ModelName != "gpt-c" || m.ProviderDefaults["openai"] != "gpt-a" || !catalog.LiveConnection {
		t.Fatalf("catalog = %+v", m)
	}
	h.fake().Handle(codexappserver.MethodModelList, func(json.RawMessage) (any, error) { return nil, errors.New("down") })
	if code := h.call(http.MethodGet, "/catalog", nil, nil); code != 500 {
		t.Fatalf("failed catalog = %d", code)
	}
	h.fake().Handle(codexappserver.MethodThreadStart, func(json.RawMessage) (any, error) { return nil, errors.New("down") })
	if code := h.call(http.MethodPost, "/sessions", platforms.CreateSessionRequest{Directory: "/repo"}, nil); code != 500 {
		t.Fatalf("failed create = %d", code)
	}
	h.fake().Handle(codexappserver.MethodThreadList, func(json.RawMessage) (any, error) { return nil, errors.New("down") })
	if code := h.call(http.MethodGet, "/sessions", nil, nil); code != 500 {
		t.Fatalf("failed list = %d", code)
	}
	if !strings.Contains(perr.Code, "creation_rejected") {
		t.Fatalf("relative create code = %q", perr.Code)
	}
}
