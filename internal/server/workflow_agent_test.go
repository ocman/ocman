package server

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/workflows"
)

func TestWorkflowAgentCollectors(t *testing.T) {
	dir := initWorktreeTestRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "result.json"), []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	detail := &platforms.SessionDetail{
		Messages: []db.Message{
			{ID: "old", TimeCreated: 1, Data: json.RawMessage(`{"role":"assistant"}`)},
			{ID: "latest", TimeCreated: 2, Data: json.RawMessage(`{"role":"assistant"}`)},
		},
		Parts: []db.Part{
			{MessageID: "old", Data: json.RawMessage(`{"type":"text","text":"old"}`)},
			{MessageID: "latest", Data: json.RawMessage(`{"type":"text","text":"line one"}`)},
			{MessageID: "latest", Data: json.RawMessage(`{"type":"text","text":"line two"}`)},
		},
	}
	executor := &workflowAgentExecutor{s: newWorkflowTestServer(t)}
	tests := []struct {
		collector workflows.Collector
		contains  string
	}{
		{workflows.Collector{Name: "message", Type: "final-message"}, `"line one\nline two"`},
		{workflows.Collector{Name: "patch", Type: "diff"}, `"path":"README.md"`},
		{workflows.Collector{Name: "notes", Type: "file", Path: "notes.txt"}, `"notes"`},
		{workflows.Collector{Name: "result", Type: "json-file", Path: "result.json"}, `{"ok":true}`},
	}
	for _, tt := range tests {
		t.Run(tt.collector.Type, func(t *testing.T) {
			got, err := executor.collect(t.Context(), dir, detail, tt.collector)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(got), tt.contains) {
				t.Fatalf("collector output %s does not contain %s", got, tt.contains)
			}
		})
	}

	if err := os.WriteFile(filepath.Join(dir, "invalid.json"), []byte(`{`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.collect(t.Context(), dir, detail, workflows.Collector{Name: "invalid", Type: "json-file", Path: "invalid.json"}); err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("expected JSON validation error, got %v", err)
	}
}

func TestWorkflowAgentStartKeepsCreatedSessionWhenPromptFails(t *testing.T) {
	p := &fakePlatform{
		id:       "fake",
		sessions: []db.Session{{ID: "created", Platform: "fake"}},
		createSessionFn: func(platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
			return &platforms.CreateSessionResponse{ID: "created"}, nil
		},
		sendMessageFn: func(platforms.SendMessageRequest) error { return errors.New("send failed") },
	}
	registry := platforms.NewRegistry()
	registry.Register(p)
	srv := New(nil, openWatcherTestStateDB(t), "", registry, nil)
	session, err := (&workflowAgentExecutor{s: srv}).Start(t.Context(), workflows.AgentRequest{Platform: "fake", Directory: t.TempDir(), Prompt: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if session.ID != "created" || session.State != "error" || session.Error != "send failed" {
		t.Fatalf("created session was lost after send failure: %+v", session)
	}
}

func TestWorkflowAgentStartSendsCorrectionToExistingSession(t *testing.T) {
	const correction = "Your previous response was not valid JSON. Reply once with only a valid JSON value for the workflow result."
	var sent platforms.SendMessageRequest
	p := &fakePlatform{
		id: "fake",
		sendMessageFn: func(req platforms.SendMessageRequest) error {
			sent = req
			return nil
		},
	}
	registry := platforms.NewRegistry()
	registry.Register(p)
	srv := New(nil, openWatcherTestStateDB(t), "", registry, nil)
	session, err := (&workflowAgentExecutor{s: srv}).Start(t.Context(), workflows.AgentRequest{Platform: "fake", Directory: t.TempDir(), Prompt: correction, SessionID: "session-1"})
	if err != nil {
		t.Fatal(err)
	}
	if session.ID != "session-1" || sent.SessionID != "session-1" || sent.Message != correction {
		t.Fatalf("correction was not sent to the existing session: session=%+v request=%+v", session, sent)
	}
}

func TestWorkflowAgentInspectReturnsFinalMessage(t *testing.T) {
	p := &fakePlatform{
		id: "fake",
		sessionDetailFn: func(string) (*platforms.SessionDetail, error) {
			return &platforms.SessionDetail{
				Session:  &db.Session{ID: "session-1", Status: "done"},
				Messages: []db.Message{{ID: "message-1", TimeCreated: 1, Data: json.RawMessage(`{"role":"assistant"}`)}},
				Parts:    []db.Part{{MessageID: "message-1", Data: json.RawMessage(`{"type":"text","text":"{\"ok\":true}"}`)}},
			}, nil
		},
	}
	registry := platforms.NewRegistry()
	registry.Register(p)
	srv := New(nil, openWatcherTestStateDB(t), "", registry, nil)
	result, err := (&workflowAgentExecutor{s: srv}).Inspect(t.Context(), workflows.AgentSession{ID: "session-1", Platform: "fake"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "done" || result.FinalMessage != `{"ok":true}` {
		t.Fatalf("agent result = %+v", result)
	}
}

func TestWorkflowAgentCancelUsesSessionService(t *testing.T) {
	var aborted string
	p := &fakePlatform{id: "fake", abortFn: func(req platforms.AbortRequest) error {
		aborted = req.SessionID
		return nil
	}}
	registry := platforms.NewRegistry()
	registry.Register(p)
	srv := New(nil, openWatcherTestStateDB(t), "", registry, nil)
	if err := (&workflowAgentExecutor{s: srv}).Cancel(t.Context(), workflows.AgentSession{ID: "session-1", Platform: "fake"}); err != nil {
		t.Fatal(err)
	}
	if aborted != "session-1" {
		t.Fatalf("abort session = %q", aborted)
	}
}
