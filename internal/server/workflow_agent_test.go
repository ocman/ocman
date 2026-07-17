package server

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/workflows"
)

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
	result, err := (&workflowAgentExecutor{s: srv}).Inspect(t.Context(), workflows.AgentSession{ID: "session-1", Platform: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "done" || result.FinalMessage != `{"ok":true}` {
		t.Fatalf("agent result = %+v", result)
	}
}

func TestWorkflowAgentInspectWaitsForCorrectionResponse(t *testing.T) {
	messages := []db.Message{
		{ID: "prompt", TimeCreated: 1, Data: json.RawMessage(`{"role":"user"}`)},
		{ID: "invalid", TimeCreated: 2, Data: json.RawMessage(`{"role":"assistant"}`)},
		{ID: "correction", TimeCreated: 3, Data: json.RawMessage(`{"role":"user"}`)},
	}
	parts := []db.Part{{MessageID: "invalid", Data: json.RawMessage(`{"type":"text","text":"not json"}`)}}
	p := &fakePlatform{
		id: "fake",
		sessionDetailFn: func(string) (*platforms.SessionDetail, error) {
			return &platforms.SessionDetail{
				Session:  &db.Session{ID: "session-1", Status: "done"},
				Messages: messages,
				Parts:    parts,
			}, nil
		},
	}
	registry := platforms.NewRegistry()
	registry.Register(p)
	srv := New(nil, openWatcherTestStateDB(t), "", registry, nil)
	result, err := (&workflowAgentExecutor{s: srv}).Inspect(t.Context(), workflows.AgentSession{ID: "session-1", Platform: "fake", State: workflows.AgentCorrecting})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "busy" || result.FinalMessage != "" {
		t.Fatalf("stale response was consumed during correction: %+v", result)
	}
	messages = append(messages, db.Message{ID: "corrected", TimeCreated: 4, Data: json.RawMessage(`{"role":"assistant"}`)})
	parts = append(parts, db.Part{MessageID: "corrected", Data: json.RawMessage(`{"type":"text","text":"{\"ok\":true}"}`)})
	result, err = (&workflowAgentExecutor{s: srv}).Inspect(t.Context(), workflows.AgentSession{ID: "session-1", Platform: "fake", State: workflows.AgentCorrecting})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "done" || result.FinalMessage != `{"ok":true}` {
		t.Fatalf("corrected response was not consumed: %+v", result)
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
