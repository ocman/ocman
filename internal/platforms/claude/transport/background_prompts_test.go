package claudecode

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestBackgroundQuestionAppearsInSessionList(t *testing.T) {
	e := newRecoveryEnv(t)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(e.data, "config"))
	fixture := filepath.Join(e.data, "question.jsonl")
	if err := os.WriteFile(fixture, []byte(`{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"control_request","request_id":"question","request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion","tool_use_id":"tool_question","input":{"questions":[{"question":"Which option?","header":"Option","options":[{"label":"A","description":"First"},{"label":"B","description":"Second"}]}]}}}}
{"direction":"stdin","message":{"type":"control_response"}}
{"direction":"stdout","message":{"type":"result","subtype":"success","is_error":false,"result":"Done"}}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_CLAUDE_FIXTURE", fixture)
	p, stop := e.start(t, "127.0.0.1:47231")
	defer stop()
	created, err := p.CreateSession(t.Context(), platforms.CreateSessionRequest{Directory: e.repo})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: created.ID, Message: "go"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "background question", func() bool {
		live := p.LiveStatus(created.ID)
		return live != nil && live.PendingQuestion
	})
	listed := func() db.Session {
		t.Helper()
		rows, err := p.Sessions(t.Context(), "", 0)
		if err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(rows, func(s db.Session) bool { return s.ID == created.ID })
		if i < 0 {
			t.Fatal("session missing from list")
		}
		return rows[i]
	}
	if row := listed(); row.Status != db.StatusWaiting || !row.PendingQuestion || row.PendingPermission {
		t.Fatalf("background question not surfaced: %+v", row)
	}
	questions, err := p.ListQuestions(t.Context(), created.ID)
	if err != nil || len(questions) != 1 {
		t.Fatalf("questions = %+v, %v", questions, err)
	}
	if err := p.RespondQuestion(t.Context(), platforms.RespondQuestionRequest{SessionID: created.ID, RequestID: questions[0]["id"].(string), Answers: [][]string{{"A"}}}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "question answered", func() bool { return !p.LiveStatus(created.ID).PendingQuestion })
	if row := listed(); row.PendingQuestion || row.PendingPermission {
		t.Fatalf("answered prompt still surfaced: %+v", row)
	}
}
