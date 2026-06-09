package server

import (
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
)

func mkMessage(id, role string, t int64, data string) db.Message {
	body := `{"role":"` + role + `"`
	if data != "" {
		body += "," + data
	}
	body += "}"
	return db.Message{ID: id, SessionID: "ses_1", TimeCreated: t, Data: []byte(body)}
}

func mkPart(id, messageID string, t int64, data string) db.Part {
	return db.Part{ID: id, MessageID: messageID, SessionID: "ses_1", TimeCreated: t, Data: []byte(data)}
}

func TestConversationMarkdown_Basic(t *testing.T) {
	session := &db.Session{
		ID:           "ses_1",
		Title:        "My Conversation",
		Directory:    "/home/u/proj",
		TimeCreated:  1_700_000_000_000,
		MessageCount: 2,
	}
	messages := []db.Message{
		mkMessage("m1", "user", 100, ""),
		mkMessage("m2", "assistant", 200, `"modelID":"claude-opus-4"`),
	}
	parts := []db.Part{
		mkPart("p1", "m1", 100, `{"type":"text","text":"Hello there"}`),
		mkPart("p2", "m2", 200, `{"type":"text","text":"Hi! How can I help?"}`),
	}

	md := conversationMarkdown(session, messages, parts)

	for _, want := range []string{
		"# My Conversation",
		"**Directory:** `/home/u/proj`",
		"## User",
		"Hello there",
		"## Assistant (claude-opus-4)",
		"Hi! How can I help?",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q\n---\n%s", want, md)
		}
	}

	// User section must come before assistant section.
	if strings.Index(md, "## User") > strings.Index(md, "## Assistant") {
		t.Error("expected user section before assistant section")
	}
}

func TestConversationMarkdown_ChronologicalOrder(t *testing.T) {
	// Messages provided out of order must be rendered by timeCreated.
	messages := []db.Message{
		mkMessage("m2", "assistant", 200, ""),
		mkMessage("m1", "user", 100, ""),
	}
	parts := []db.Part{
		mkPart("p2", "m2", 200, `{"type":"text","text":"SECOND"}`),
		mkPart("p1", "m1", 100, `{"type":"text","text":"FIRST"}`),
	}
	md := conversationMarkdown(&db.Session{Title: "x"}, messages, parts)
	if strings.Index(md, "FIRST") > strings.Index(md, "SECOND") {
		t.Errorf("messages not in chronological order:\n%s", md)
	}
}

func TestConversationMarkdown_ToolPart(t *testing.T) {
	messages := []db.Message{mkMessage("m1", "assistant", 100, "")}
	parts := []db.Part{
		mkPart("p1", "m1", 100, `{"type":"tool","tool":"bash","state":{"status":"completed","title":"List files","input":{"command":"ls -la"},"output":"file1\nfile2"}}`),
	}
	md := conversationMarkdown(&db.Session{Title: "x"}, messages, parts)
	for _, want := range []string{"bash", "List files", "ls -la", "file1", "<details>"} {
		if !strings.Contains(md, want) {
			t.Errorf("tool markdown missing %q\n---\n%s", want, md)
		}
	}
}

func TestConversationMarkdown_ReasoningAsBlockquote(t *testing.T) {
	messages := []db.Message{mkMessage("m1", "assistant", 100, "")}
	parts := []db.Part{
		mkPart("p1", "m1", 100, `{"type":"reasoning","text":"Let me think\nabout this"}`),
	}
	md := conversationMarkdown(&db.Session{Title: "x"}, messages, parts)
	if !strings.Contains(md, "> **Reasoning**") {
		t.Errorf("expected reasoning blockquote header\n%s", md)
	}
	if !strings.Contains(md, "> Let me think") {
		t.Errorf("expected reasoning content quoted\n%s", md)
	}
}

func TestConversationMarkdown_SkipsStructuralParts(t *testing.T) {
	messages := []db.Message{mkMessage("m1", "assistant", 100, "")}
	parts := []db.Part{
		mkPart("p1", "m1", 100, `{"type":"step-start"}`),
		mkPart("p2", "m1", 101, `{"type":"step-finish"}`),
	}
	md := conversationMarkdown(&db.Session{Title: "x"}, messages, parts)
	if !strings.Contains(md, "_(no content)_") {
		t.Errorf("expected no-content placeholder when only structural parts present\n%s", md)
	}
}

func TestConversationMarkdown_TolerantOfBadPartJSON(t *testing.T) {
	messages := []db.Message{mkMessage("m1", "user", 100, "")}
	parts := []db.Part{
		mkPart("p1", "m1", 100, `{not valid json`),
		mkPart("p2", "m1", 101, `{"type":"text","text":"still here"}`),
	}
	md := conversationMarkdown(&db.Session{Title: "x"}, messages, parts)
	if !strings.Contains(md, "still here") {
		t.Errorf("expected valid part to survive a malformed sibling\n%s", md)
	}
}

func TestConversationMarkdown_NilSession(t *testing.T) {
	md := conversationMarkdown(nil, nil, nil)
	if !strings.Contains(md, "# Conversation") {
		t.Errorf("expected fallback title for nil session\n%s", md)
	}
}
