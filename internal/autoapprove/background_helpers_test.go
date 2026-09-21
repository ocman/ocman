package autoapprove

import (
	"bytes"
	"context"
	"strings"
	"testing"

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
	sections := svc.resolveJudgeSections(t.Context(), inputs.directory, "session")
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
