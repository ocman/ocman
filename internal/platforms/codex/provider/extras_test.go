package codexprovider

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	codexappserver "github.com/NoUseFreak/ocman/internal/platforms/codex/appserver"
	"github.com/NoUseFreak/ocman/internal/platforms/codex/appserver/codextest"
)

// TestRecordedExtras replays the 0.164.0 extras recording over the protocol
// socket: a turn, a `!` shell command, /compact and /review each run as a
// turn that settles, and forks map ocman messages to Codex turns.
func TestRecordedExtras(t *testing.T) {
	r := &replayer{t: t, rec: loadRecording(t, extras), calls: map[string]int{}}
	h := newHarness(t, r.setup)
	thread := load[codexappserver.ThreadReadResponse](t, extras+"thread-read.json").Thread
	var created platforms.CreateSessionResponse
	if code := h.call(http.MethodPost, "/sessions", platforms.CreateSessionRequest{Directory: "/fixture/repo"}, &created); code != 200 || created.ID != thread.ID {
		t.Fatalf("create = %d %+v", code, created)
	}
	id := created.ID
	idle := func(what string) {
		t.Helper()
		h.next("session.idle", func(p map[string]any) bool { return p["sessionID"] == id })
	}

	h.call(http.MethodPost, "/sessions/"+id+"/send", platforms.SendMessageRequest{Message: "Reply with the word ready."}, nil)
	h.next("message.part.updated", func(p map[string]any) bool { return part(p)["text"] == "ready" })
	idle("turn")

	if code := h.call(http.MethodPost, "/sessions/"+id+"/shell", platforms.RunShellRequest{Command: "echo shell-ok"}, nil); code != 204 {
		t.Fatalf("shell = %d", code)
	}
	shell := h.next("message.part.updated", func(p map[string]any) bool { return part(p)["tool"] == "bash" })
	if st := part(shell)["state"].(map[string]any); st["metadata"].(map[string]any)["ocmanUserExecutedShell"] != true ||
		st["input"].(map[string]any)["command"] != "echo shell-ok" {
		t.Fatalf("shell part = %v", shell)
	}
	idle("shell")

	if code := h.call(http.MethodPost, "/sessions/"+id+"/compact", platforms.CompactRequest{SessionID: id}, nil); code != 204 {
		t.Fatalf("compact = %d", code)
	}
	h.next("message.part.updated", func(p map[string]any) bool { return part(p)["text"] == "Conversation compacted." })
	idle("compact")

	review := platforms.ExecuteCommandRequest{Command: "review", Arguments: "Reply with the word LGTM. Do not run any tools."}
	if code := h.call(http.MethodPost, "/sessions/"+id+"/command", review, nil); code != 204 {
		t.Fatalf("review = %d", code)
	}
	h.next("message.part.updated", func(p map[string]any) bool { return part(p)["text"] == "LGTM" })
	idle("review")

	var calls []string
	var reviewParams string
	for _, c := range h.fake().Calls() {
		calls = append(calls, c.Method)
		if c.Method == codexappserver.MethodReviewStart {
			reviewParams = string(c.Params)
		}
	}
	for _, m := range []string{codexappserver.MethodShellCommand, codexappserver.MethodThreadCompact, codexappserver.MethodReviewStart} {
		if !slices.Contains(calls, m) {
			t.Errorf("%s was not called: %q", m, calls)
		}
	}
	if reviewParams != `{"threadId":"`+id+`","target":{"type":"custom","instructions":"Reply with the word LGTM. Do not run any tools."}}` {
		t.Errorf("review params = %s", reviewParams)
	}

	// Fork whole, then from the shell turn's message: through the turn before.
	var forked platforms.CreateSessionResponse
	if code := h.call(http.MethodPost, "/sessions/"+id+"/fork", platforms.ForkSessionRequest{}, &forked); code != 200 || forked.ID == "" || forked.ID == id {
		t.Fatalf("fork = %d %+v", code, forked)
	}
	items := load[struct{ Data []codexappserver.ItemEntry }](t, extras+"items.json").Data
	turns := load[struct{ Data []codexappserver.Turn }](t, extras+"turns.json").Data
	shellItem := items[2] // the user shell command, in the second turn
	if code := h.call(http.MethodPost, "/sessions/"+id+"/fork", platforms.ForkSessionRequest{MessageID: "msg_" + shellItem.Item.ID}, &forked); code != 200 {
		t.Fatalf("fork at message = %d", code)
	}
	var forkParams []string
	for _, c := range h.fake().Calls() {
		if c.Method == codexappserver.MethodThreadFork {
			forkParams = append(forkParams, string(c.Params))
		}
	}
	want := []string{`{"threadId":"` + id + `","excludeTurns":true}`, `{"threadId":"` + id + `","lastTurnId":"` + turns[0].ID + `","excludeTurns":true}`}
	if !slices.Equal(forkParams, want) {
		t.Fatalf("fork params = %q, want %q", forkParams, want)
	}
}

func TestForkEdges(t *testing.T) {
	h := newHarness(t, func(s *codextest.Server) {
		scripted(s)
		s.Handle(codexappserver.MethodThreadItems, func(json.RawMessage) (any, error) {
			return map[string]any{"data": []any{
				map[string]any{"turnId": "t1", "item": map[string]any{"type": "userMessage", "id": "u1"}},
				map[string]any{"turnId": "t2", "item": map[string]any{"type": "userMessage", "id": "u2"}}}}, nil
		})
		s.Handle(codexappserver.MethodThreadTurns, func(json.RawMessage) (any, error) {
			return map[string]any{"data": []any{map[string]any{"id": "t1", "status": "completed"}, map[string]any{"id": "t2", "status": "failed"},
				map[string]any{"id": "t3", "status": "failed"}}}, nil
		})
		s.Handle(codexappserver.MethodThreadRead, func(json.RawMessage) (any, error) { return map[string]any{"thread": thread("thr")}, nil })
	})
	var out platforms.CreateSessionResponse
	// The first turn's message: nothing before it, so a fresh thread.
	if code := h.call(http.MethodPost, "/sessions/thr/fork", platforms.ForkSessionRequest{MessageID: "msg_u1"}, &out); code != 200 || out.ID != "thr" {
		t.Fatalf("fork at first message = %d %+v", code, out)
	}
	if !slices.ContainsFunc(h.fake().Calls(), func(c codextest.Call) bool { return c.Method == codexappserver.MethodThreadStart }) {
		t.Fatal("fork at the first message did not start an empty thread")
	}
	// A failed turn's synthesized reply, msg_<turn id>, maps to its turn.
	if code := h.call(http.MethodPost, "/sessions/thr/fork", platforms.ForkSessionRequest{MessageID: "msg_t3"}, nil); code != 500 {
		t.Fatalf("fork at a failed reply = %d (thread/fork is not scripted)", code)
	}
	var perr struct{ Code string }
	if code := h.call(http.MethodPost, "/sessions/thr/fork", platforms.ForkSessionRequest{MessageID: "msg_nope"}, &perr); code != 404 || perr.Code != "creation_rejected" {
		t.Fatalf("fork at an unknown message = %d %q", code, perr.Code)
	}
	if code := h.call(http.MethodPost, "/sessions/thr/fork", "bad", nil); code != 400 {
		t.Fatalf("bad fork body = %d", code)
	}
	if code := h.call(http.MethodPost, "/sessions/thr/shell", platforms.RunShellRequest{Command: " "}, nil); code != 400 {
		t.Fatalf("empty shell = %d", code)
	}
	if code := h.call(http.MethodPost, "/sessions/thr/command", platforms.ExecuteCommandRequest{Command: "nope"}, nil); code != 500 {
		t.Fatalf("command when skills cannot be listed = %d", code)
	}
	h.fake().Handle(codexappserver.MethodSkillsList, func(json.RawMessage) (any, error) {
		return map[string]any{"data": []any{map[string]any{"cwd": "/repo", "skills": []any{map[string]any{"name": "pdf", "path": "/s/pdf/SKILL.md", "enabled": true}}}}}, nil
	})
	if code := h.call(http.MethodPost, "/sessions/thr/command", platforms.ExecuteCommandRequest{Command: "nope"}, nil); code != 404 {
		t.Fatalf("unknown command = %d", code)
	}
	// A skill command sends the skill and its arguments as one turn.
	if code := h.call(http.MethodPost, "/sessions/thr/command", platforms.ExecuteCommandRequest{Command: "/pdf", Arguments: "merge a.pdf b.pdf"}, nil); code != 204 {
		t.Fatalf("skill command = %d", code)
	}
	var turn codexappserver.TurnStartParams
	for _, c := range h.fake().Calls() {
		if c.Method == codexappserver.MethodTurnStart {
			_ = json.Unmarshal(c.Params, &turn)
		}
	}
	if len(turn.Input) != 2 || turn.Input[0].Type != "skill" || turn.Input[0].Path != "/s/pdf/SKILL.md" || turn.Input[1].Text != "merge a.pdf b.pdf" {
		t.Fatalf("skill turn = %+v", turn.Input)
	}
	// A running turn refuses compaction and shell until it settles.
	for _, path := range []string{"/compact", "/shell", "/command"} {
		body := any(platforms.RunShellRequest{Command: "ls"})
		if path == "/command" {
			body = platforms.ExecuteCommandRequest{Command: "compact"}
		}
		if code := h.call(http.MethodPost, "/sessions/thr"+path, body, nil); code != 409 {
			t.Errorf("%s while busy = %d", path, code)
		}
	}
	notify(t, h.fake(), codexappserver.NotifyTurnCompleted, map[string]any{"threadId": "thr", "turn": map[string]any{"id": "t1", "status": "completed"}})
	h.next("session.idle", nil)
	if code := h.call(http.MethodPost, "/sessions/thr/command", platforms.ExecuteCommandRequest{Command: "review"}, nil); code != 500 {
		t.Fatalf("review without a scripted review/start = %d", code)
	}
	h.next("session.status", func(p map[string]any) bool { return p["status"].(map[string]any)["type"] == "error" })
}
