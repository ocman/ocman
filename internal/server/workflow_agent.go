package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/workflows"
)

const workflowEngineTickInterval = 5 * time.Second

type workflowAgentExecutor struct{ s *Server }

func (e *workflowAgentExecutor) Start(ctx context.Context, req workflows.AgentRequest) (workflows.AgentSession, error) {
	platformID := req.Platform
	if req.SessionID != "" {
		sendErr := e.s.sessions.SendMessage(ctx, platformID, platforms.SendMessageRequest{SessionID: req.SessionID, Message: req.Prompt, Model: req.Model, Agent: req.Agent, Reasoning: req.Reasoning})
		if platformID == "" {
			if p, ok := e.s.registry.PlatformForSession(ctx, req.SessionID); ok {
				platformID = string(p.ID())
			}
		}
		session := workflows.AgentSession{ID: req.SessionID, Platform: platformID, State: "busy", Directory: req.Directory}
		if sendErr != nil {
			session.State, session.Error = "error", sendErr.Error()
		}
		return session, nil
	}
	resp, err := e.s.sessions.Create(ctx, platformID, platforms.CreateSessionRequest{Directory: req.Directory})
	if err != nil {
		return workflows.AgentSession{}, err
	}
	if platformID == "" {
		if p, ok := e.s.registry.PlatformForSession(ctx, resp.ID); ok {
			platformID = string(p.ID())
		}
	}
	session := workflows.AgentSession{ID: resp.ID, Platform: platformID, State: "busy", Directory: req.Directory}
	if err := e.s.sessions.SendMessage(ctx, platformID, platforms.SendMessageRequest{SessionID: resp.ID, Message: req.Prompt, Model: req.Model, Agent: req.Agent, Reasoning: req.Reasoning}); err != nil {
		session.State, session.Error = "error", err.Error()
	}
	return session, nil
}

func (e *workflowAgentExecutor) Inspect(ctx context.Context, session workflows.AgentSession) (workflows.AgentResult, error) {
	p, ok := e.s.registry.Get(platforms.ID(session.Platform))
	if !ok {
		return workflows.AgentResult{}, fmt.Errorf("workflow agent platform %q is unavailable", session.Platform)
	}
	detail, err := p.Session(ctx, session.ID, 1000, 0)
	if err != nil {
		return workflows.AgentResult{}, err
	}
	if detail == nil || detail.Session == nil {
		return workflows.AgentResult{}, fmt.Errorf("workflow agent session %q was not found", session.ID)
	}
	result := workflows.AgentResult{State: string(detail.Session.Status)}
	if result.State == string(db.StatusBusy) {
		return result, nil
	}
	// The agent process owning this turn is gone, so the node will never
	// get a final message. Fail it rather than fall through and harvest a
	// half-finished answer as if it were complete.
	if result.State == string(db.StatusInterrupted) {
		result.State = string(db.StatusError)
		result.Error = "session was interrupted before the turn finished"
		return result, nil
	}
	if result.State == string(db.StatusError) {
		result.Error = detail.Session.LastErrorMessage
		return result, nil
	}
	if session.State == workflows.AgentCorrecting {
		var ok bool
		result.FinalMessage, ok = correctionAssistantMessage(detail.Messages, detail.Parts)
		if !ok {
			result.State = "busy"
		}
		return result, nil
	}
	result.FinalMessage = finalAssistantMessage(detail.Messages, detail.Parts)
	return result, nil
}

func (e *workflowAgentExecutor) Cancel(ctx context.Context, session workflows.AgentSession) error {
	return e.s.sessions.Abort(ctx, session.Platform, platforms.AbortRequest{SessionID: session.ID})
}

func finalAssistantMessage(messages []db.Message, parts []db.Part) string {
	text, _ := assistantMessage(messages, parts)
	return text
}

func correctionAssistantMessage(messages []db.Message, parts []db.Part) (string, bool) {
	start := 0
	for i, message := range messages {
		var data db.MessageData
		if json.Unmarshal(message.Data, &data) == nil && data.Role == "user" {
			start = i + 1
		}
	}
	return assistantMessage(messages[start:], parts)
}

func assistantMessage(messages []db.Message, parts []db.Part) (string, bool) {
	var latest db.Message
	for _, message := range messages {
		var data db.MessageData
		if json.Unmarshal(message.Data, &data) == nil && data.Role == "assistant" && message.TimeCreated >= latest.TimeCreated {
			latest = message
		}
	}
	var text []string
	for _, part := range parts {
		if part.MessageID != latest.ID {
			continue
		}
		var data struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(part.Data, &data) == nil && data.Type == "text" && data.Text != "" {
			text = append(text, data.Text)
		}
	}
	return strings.Join(text, "\n"), latest.ID != ""
}

func (s *Server) runWorkflowEngine(ctx context.Context) {
	if s.stateDB == nil {
		return
	}
	ticker := time.NewTicker(workflowEngineTickInterval)
	defer ticker.Stop()
	for {
		runWithRecover("workflow-engine", func() {
			if err := s.workflowSvc().Tick(ctx); err != nil {
				log.WithError(err).Warn("workflow-engine: tick")
			}
		})
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
