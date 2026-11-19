package claudeprovider

import (
	"encoding/json"
	"strings"
	"testing"
)

func toolOutput(t *testing.T, c *Converter) string {
	t.Helper()
	c.Finish()
	var part struct {
		State struct {
			Output string `json:"output"`
		} `json:"state"`
	}
	if len(c.Parts) != 1 {
		t.Fatalf("parts = %d, want 1", len(c.Parts))
	}
	if err := json.Unmarshal(c.Parts[0].Data, &part); err != nil {
		t.Fatal(err)
	}
	return part.State.Output
}

func TestTurnNoteFootnotesToolOutput(t *testing.T) {
	c := &Converter{SessionID: "session"}
	c.Add([]byte(`{"type":"assistant","message":{"id":"message","content":[{"type":"tool_use","id":"tool","name":"Read","input":{}}]}}`))
	c.Add([]byte(`{"type":"user","uuid":"result","message":{"content":[{"type":"tool_result","tool_use_id":"tool","content":"file text"}]}}`))
	c.Add([]byte(`{"type":"user","isMeta":true,"turnCompanion":true,"parentUuid":"result","message":{"role":"user","content":"[Image: original 3354x140, displayed at 2000x83. Multiply coordinates by 1.68 to map to original image.]"}}`))
	got := toolOutput(t, c)
	if got != "file text\n\n[Image: original 3354x140, displayed at 2000x83. Multiply coordinates by 1.68 to map to original image.]" {
		t.Fatalf("output = %q", got)
	}
}

func TestTurnNoteKeepsOnlyAllowlistedText(t *testing.T) {
	for name, content := range map[string]string{
		"unknown companion": `"Some other companion note"`,
		"empty":             `""`,
	} {
		t.Run(name, func(t *testing.T) {
			c := &Converter{SessionID: "session"}
			c.Add([]byte(`{"type":"assistant","message":{"id":"message","content":[{"type":"tool_use","id":"tool","name":"Read","input":{}}]}}`))
			c.Add([]byte(`{"type":"user","uuid":"result","message":{"content":[{"type":"tool_result","tool_use_id":"tool","content":"file text"}]}}`))
			c.Add([]byte(`{"type":"user","isMeta":true,"turnCompanion":true,"parentUuid":"result","message":{"role":"user","content":` + content + `}}`))
			if got := toolOutput(t, c); got != "file text" {
				t.Fatalf("output = %q", got)
			}
		})
	}
}

func TestTurnNoteWithoutMatchingResultIsDropped(t *testing.T) {
	c := &Converter{SessionID: "session"}
	c.Add([]byte(`{"type":"assistant","message":{"id":"message","content":[{"type":"tool_use","id":"tool","name":"Read","input":{}}]}}`))
	c.Add([]byte(`{"type":"user","uuid":"result","message":{"content":[{"type":"tool_result","tool_use_id":"tool","content":"file text"}]}}`))
	c.Add([]byte(`{"type":"user","isMeta":true,"turnCompanion":true,"parentUuid":"attachment-record","message":{"role":"user","content":"[Image: original 1x1]"}}`))
	if got := toolOutput(t, c); strings.Contains(got, "Image") {
		t.Fatalf("unmatched note attached: %q", got)
	}
}

func TestMetaLinesWithoutCompanionFlagStayHidden(t *testing.T) {
	c := &Converter{SessionID: "session"}
	c.Add([]byte(`{"type":"assistant","message":{"id":"message","content":[{"type":"tool_use","id":"tool","name":"Read","input":{}}]}}`))
	c.Add([]byte(`{"type":"user","uuid":"result","message":{"content":[{"type":"tool_result","tool_use_id":"tool","content":"file text"}]}}`))
	c.Add([]byte(`{"type":"user","isMeta":true,"parentUuid":"result","message":{"role":"user","content":"[Image: original 1x1]"}}`))
	if got := toolOutput(t, c); got != "file text" {
		t.Fatalf("output = %q", got)
	}
}

func TestTurnNoteIsNotAGroupBoundary(t *testing.T) {
	if kind := classify(&line{Type: "user", IsMeta: true, TurnCompanion: true}, "message"); kind != continueGroup {
		t.Fatalf("companion classified as %d, want continueGroup", kind)
	}
	if kind := classify(&line{Type: "user", IsMeta: true}, "message"); kind != skipLine {
		t.Fatalf("plain meta classified as %d, want skipLine", kind)
	}
}

// Live stream-json sends a model-invoked skill's body as a synthetic user line
// without isMeta or turnCompanion. It must not open a user message: the
// frontend would draw it as a second Skill line next to the Skill call.
func TestStreamSkillBodyIsNotAUserMessage(t *testing.T) {
	c := &Converter{SessionID: "session"}
	c.Add([]byte(`{"type":"assistant","message":{"id":"message","content":[{"type":"tool_use","id":"tool","name":"Skill","input":{"skill":"create-pr"}}]}}`))
	c.Add([]byte(`{"type":"user","uuid":"result","parent_tool_use_id":null,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool","content":"Launching skill: create-pr"}]}}`))
	c.Add([]byte(`{"type":"user","uuid":"body","isSynthetic":true,"parent_tool_use_id":null,"message":{"role":"user","content":[{"type":"text","text":"Base directory for this skill: /home/u/.claude/skills/create-pr\n\nCreate the PR."}]}}`))
	if got := toolOutput(t, c); got != "Launching skill: create-pr" {
		t.Fatalf("output = %q", got)
	}
	if len(c.Messages) != 1 {
		t.Fatalf("messages = %d, want only the assistant message", len(c.Messages))
	}
}

func TestSyntheticUserPromptStillStartsAMessage(t *testing.T) {
	c := &Converter{SessionID: "session"}
	c.Add([]byte(`{"type":"user","uuid":"prompt","isSynthetic":true,"message":{"role":"user","content":"<task-notification>done</task-notification>"}}`))
	c.Finish()
	if len(c.Messages) != 1 {
		t.Fatalf("messages = %d, want the synthetic prompt", len(c.Messages))
	}
}
