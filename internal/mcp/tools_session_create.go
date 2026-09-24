package mcp

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/NoUseFreak/ocman/internal/platforms"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// ErrInvalidSessionCreate marks a create failure caused by the caller's
// input; its message is safe to return to the agent.
var ErrInvalidSessionCreate = errors.New("invalid session request")

// CreateSessionRequest starts a new session and sends it one prompt.
// Directory wins; when empty the service resolves the project root of the
// calling session (Platform + SessionID), which also picks the owner.
type CreateSessionRequest struct {
	Prompt, Model, Agent, Title string
	Directory                   string
	Platform, SessionID         string
}

// CreatedSession is the sessions create result.
type CreatedSession struct {
	Platform  string `json:"platform"`
	SessionID string `json:"session_id"`
	Directory string `json:"directory"`
}

type sessionCreator interface {
	CreateSession(context.Context, CreateSessionRequest) (CreatedSession, error)
}

func (t *sessionTools) create(ctx context.Context, req mcplib.CallToolRequest) *mcplib.CallToolResult {
	creator, ok := t.svc.(sessionCreator)
	if !ok {
		return mcplib.NewToolResultError("session creation is unavailable")
	}
	in := CreateSessionRequest{
		Prompt: strings.TrimSpace(req.GetString("prompt", "")), Model: strings.TrimSpace(req.GetString("model", "")),
		Agent: strings.TrimSpace(req.GetString("agent", "")), Title: strings.TrimSpace(req.GetString("title", "")),
		Directory: strings.TrimSpace(req.GetString("directory", "")),
		Platform:  strings.TrimSpace(req.GetString("platform", "")), SessionID: strings.TrimSpace(req.GetString("session_id", "")),
	}
	switch {
	case in.Prompt == "":
		return mcplib.NewToolResultError("prompt is required")
	case in.Model != "" && !strings.Contains(strings.Trim(in.Model, "/"), "/"):
		return mcplib.NewToolResultError("model must be provider/model")
	case in.Directory != "" && !filepath.IsAbs(in.Directory):
		return mcplib.NewToolResultError("directory must be absolute")
	case (in.Platform == "") != (in.SessionID == ""):
		return mcplib.NewToolResultError("platform and session_id must be provided together")
	case in.Directory == "" && in.SessionID == "":
		return mcplib.NewToolResultError("directory or the calling session's platform and session_id is required")
	}
	created, err := creator.CreateSession(ctx, in)
	switch {
	case err != nil && created.SessionID != "":
		// Don't hide the session: the agent can retry the prompt in the UI or inspect it.
		return mcplib.NewToolResultError("session " + created.SessionID + " was created on " + created.Platform + " but the prompt failed")
	case errors.Is(err, ErrInvalidSessionCreate):
		return mcplib.NewToolResultError(err.Error())
	case errors.Is(err, platforms.ErrNotFound):
		return mcplib.NewToolResultError("session not found")
	case err != nil:
		return mcplib.NewToolResultError("session request failed")
	}
	return toolResultJSON(created)
}
