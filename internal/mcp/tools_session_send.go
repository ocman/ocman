package mcp

import (
	"context"
	"errors"
	"strings"

	"github.com/NoUseFreak/ocman/internal/platforms"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// SentSession acknowledges delivery, not completion of the recipient's turn.
type SentSession struct {
	Platform  string `json:"platform"`
	SessionID string `json:"session_id"`
	Sent      bool   `json:"sent"`
}

type sessionSender interface {
	SendSessionMessage(ctx context.Context, platform, sessionID, prompt string) (SentSession, error)
}

func (t *sessionTools) send(ctx context.Context, req mcplib.CallToolRequest) *mcplib.CallToolResult {
	sender, ok := t.svc.(sessionSender)
	if !ok {
		return mcplib.NewToolResultError("session messaging is unavailable")
	}
	id := strings.TrimSpace(req.GetString("session_id", ""))
	prompt := req.GetString("prompt", "")
	if id == "" {
		return mcplib.NewToolResultError("session_id is required")
	}
	if strings.TrimSpace(prompt) == "" {
		return mcplib.NewToolResultError("prompt is required")
	}
	platform, err := req.RequireString("platform")
	if _, present := req.GetArguments()["platform"]; present && err != nil {
		return mcplib.NewToolResultError("platform must be a string")
	}
	sent, err := sender.SendSessionMessage(ctx, strings.TrimSpace(platform), id, prompt)
	var ambiguous AmbiguousSessionError
	switch {
	case errors.Is(err, platforms.ErrNotFound):
		return mcplib.NewToolResultError("session not found")
	case errors.As(err, &ambiguous):
		return mcplib.NewToolResultError(ambiguous.Error())
	case err != nil:
		return mcplib.NewToolResultError("session request failed")
	}
	return toolResultJSON(sent)
}
