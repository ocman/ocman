package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/workflows"
)

const workflowEngineTickInterval = 5 * time.Second

type workflowAgentExecutor struct{ s *Server }

type workflowFileReader interface {
	ReadFile(context.Context, string, string) ([]byte, error)
}

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

func (e *workflowAgentExecutor) Inspect(ctx context.Context, session workflows.AgentSession, collectors []workflows.Collector) (workflows.AgentResult, error) {
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
	result := workflows.AgentResult{State: detail.Session.Status}
	if result.State == "busy" {
		return result, nil
	}
	if result.State == "error" {
		result.Error = detail.Session.LastErrorMessage
		return result, nil
	}
	result.FinalMessage = finalAssistantMessage(detail.Messages, detail.Parts)
	result.Outputs = make(map[string]json.RawMessage, len(collectors))
	for _, collector := range collectors {
		value, err := e.collect(ctx, session.Directory, detail, collector)
		if err != nil {
			return workflows.AgentResult{State: "error", Error: err.Error()}, nil
		}
		result.Outputs[collector.Name] = value
	}
	return result, nil
}

func (e *workflowAgentExecutor) collect(ctx context.Context, dir string, detail *platforms.SessionDetail, collector workflows.Collector) (json.RawMessage, error) {
	switch collector.Type {
	case "final-message":
		return json.Marshal(finalAssistantMessage(detail.Messages, detail.Parts))
	case "diff":
		host := e.s.router().ForDir(dir)
		if !host.Capabilities().GitDiff {
			return nil, fmt.Errorf("collector %q: host does not support Git diff", collector.Name)
		}
		diff, err := host.GitDiff(ctx, dir, hostsvc.GitDiffOptions{Force: true})
		if err != nil {
			return nil, fmt.Errorf("collector %q: %w", collector.Name, err)
		}
		return json.Marshal(diff)
	case "file", "json-file":
		reader, ok := e.s.router().ForDir(dir).(workflowFileReader)
		if !ok {
			return nil, fmt.Errorf("collector %q: host does not support file collection", collector.Name)
		}
		content, err := reader.ReadFile(ctx, dir, collector.Path)
		if err != nil {
			return nil, fmt.Errorf("collector %q: %w", collector.Name, err)
		}
		if collector.Type == "json-file" {
			if !json.Valid(content) {
				return nil, fmt.Errorf("collector %q: invalid JSON", collector.Name)
			}
			return json.RawMessage(content), nil
		}
		return json.Marshal(string(content))
	default:
		return nil, fmt.Errorf("collector %q: unsupported type %q", collector.Name, collector.Type)
	}
}

func (e *workflowAgentExecutor) Cancel(ctx context.Context, session workflows.AgentSession) error {
	return e.s.sessions.Abort(ctx, session.Platform, platforms.AbortRequest{SessionID: session.ID})
}

func finalAssistantMessage(messages []db.Message, parts []db.Part) string {
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
	return strings.Join(text, "\n")
}

func (s *Server) runWorkflowEngine(ctx context.Context) {
	if s.stateDB == nil {
		return
	}
	ticker := time.NewTicker(workflowEngineTickInterval)
	defer ticker.Stop()
	for {
		if err := s.workflowSvc().Tick(ctx); err != nil {
			log.WithError(err).Warn("workflow-engine: tick")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
