package autoapprove

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

type judgeInputsStore struct {
	lifecycleStore
	sections []state.PromptSection
	model    string
}

func (s *judgeInputsStore) GetPromptSections(context.Context) ([]state.PromptSection, error) {
	return s.sections, nil
}

func (s *judgeInputsStore) GetSetting(context.Context, string) (string, bool, error) {
	return s.model, s.model != "", nil
}

func TestResolveJudgeInputs(t *testing.T) {
	enabled := true
	store := &judgeInputsStore{
		lifecycleStore: lifecycleStore{delay: 125},
		sections:       []state.PromptSection{{Title: "Local policy", Content: "Read-only commands are safe.", Enabled: &enabled}},
		model:          "anthropic/test-model",
	}
	svc := NewService(Deps{Store: store})

	inputs, err := svc.resolveJudgeInputs(t.Context(), "/repo", nil, "session")
	if err != nil {
		t.Fatal(err)
	}
	if inputs.directory != "/repo" || inputs.delay != 125 {
		t.Fatalf("resolveJudgeInputs = %#v", inputs)
	}
	if len(inputs.sections) != 0 {
		t.Fatalf("prompt sections loaded before judge delay: %#v", inputs.sections)
	}
	sections := svc.resolveJudgeSections(t.Context(), nil, inputs.directory, "session")
	if len(sections) != 1 || sections[0].Title != "Local policy" || sections[0].Enabled != &enabled {
		t.Fatalf("prompt sections = %#v", sections)
	}
	svc.judge.modelMu.Lock()
	provider, model := svc.judge.modelProvider, svc.judge.modelID
	svc.judge.modelMu.Unlock()
	if provider != "anthropic" || model != "test-model" {
		t.Fatalf("judge model = %q/%q", provider, model)
	}
}

func TestHandleUnsafeVerdict(t *testing.T) {
	const sessionID, permissionID = "session", "permission"
	buf := &bytes.Buffer{}
	svc := &Service{
		sseSessions: make(map[string]map[*Sink]struct{}),
		autoApprove: make(map[string]*autoApproveStatus),
	}
	svc.RegisterSink(sessionID, buf, nil)

	svc.handleUnsafeVerdict("opencode", sessionID, permissionID, JudgeResult{Verdict: verdictUnsafe, Reasoning: "Needs human review."})

	status, ok := svc.lookupAutoApproveStatus(sessionID, permissionID)
	if !ok || status.verdict != verdictUnsafe || status.reasoning != "Needs human review." {
		t.Fatalf("unsafe status = %#v, %v", status, ok)
	}
	if event := buf.String(); !strings.Contains(event, "event: ocman.permission.flagged") || !strings.Contains(event, "Needs human review.") {
		t.Fatalf("flagged event = %q", event)
	}
}

// Without an OpenCode instance the judge reads recent user intent through
// the session's platform (Claude): user texts only, oldest first, capped.
func TestRecentUserMessagesFromSession(t *testing.T) {
	msg := func(id, role string) db.Message {
		return db.Message{ID: id, Data: json.RawMessage(`{"role":"` + role + `"}`)}
	}
	part := func(msgID, data string) db.Part { return db.Part{MessageID: msgID, Data: json.RawMessage(data)} }
	detail := &platforms.SessionDetail{
		Messages: []db.Message{msg("u0", "user"), msg("a1", "assistant"), msg("u1", "user"), msg("u2", "user")},
		Parts: []db.Part{
			part("u0", `{"type":"text","text":"first"}`),
			part("a1", `{"type":"text","text":"assistant reply"}`),
			part("u1", `{"type":"text","text":"second"}`),
			part("u1", `{"type":"text","text":"injected","synthetic":true}`),
			part("u2", `{"type":"file"}`),
		},
	}
	got := recentUserMessagesFromSession(t.Context(), &fakePlatform{detail: detail}, "s")
	if strings.Join(got, "|") != "first|second" {
		t.Fatalf("messages = %q", got)
	}
	if got := recentUserMessagesFromSession(t.Context(), &fakePlatform{}, "s"); got != nil {
		t.Fatalf("missing session = %q", got)
	}
	if got := recentUserMessagesFromSession(t.Context(), nil, "s"); got != nil {
		t.Fatalf("nil adapter = %q", got)
	}
	for i := 0; i < recentUserMessagesLimit+2; i++ {
		id := fmt.Sprint("m", i)
		detail.Messages = append(detail.Messages, msg(id, "user"))
		detail.Parts = append(detail.Parts, part(id, `{"type":"text","text":"`+id+`"}`))
	}
	if got := recentUserMessagesFromSession(t.Context(), &fakePlatform{detail: detail}, "s"); len(got) != recentUserMessagesLimit || got[len(got)-1] != fmt.Sprint("m", recentUserMessagesLimit+1) {
		t.Fatalf("capped = %q", got)
	}
}
