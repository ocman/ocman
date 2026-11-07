package mcp

import (
	"context"
	"errors"
	"strings"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/ocman/ocman/internal/platforms"
)

// ArchivedSession reports the archive state after an archive or unarchive.
// Deferred means the session was busy: it is archived on its next idle edge.
type ArchivedSession struct {
	Platform  string `json:"platform"`
	SessionID string `json:"session_id"`
	Archived  bool   `json:"archived"`
	Deferred  bool   `json:"deferred,omitempty"`
}

// ErrArchiveBusyRemote rejects archiving a busy session on a remote owner,
// whose idle edge the hub does not observe.
var ErrArchiveBusyRemote = errors.New("session is busy on a remote machine; archive it after its turn finishes")

type sessionArchiver interface {
	SetSessionArchived(ctx context.Context, platform, sessionID string, archived bool) (ArchivedSession, error)
}

func (t *sessionTools) archive(ctx context.Context, req mcplib.CallToolRequest, archived bool) *mcplib.CallToolResult {
	archiver, ok := t.svc.(sessionArchiver)
	if !ok {
		return mcplib.NewToolResultError("session archiving is unavailable")
	}
	id := strings.TrimSpace(req.GetString("session_id", ""))
	if id == "" {
		return mcplib.NewToolResultError("session_id is required")
	}
	platform, err := req.RequireString("platform")
	if _, present := req.GetArguments()["platform"]; present && err != nil {
		return mcplib.NewToolResultError("platform must be a string")
	}
	result, err := archiver.SetSessionArchived(ctx, strings.TrimSpace(platform), id, archived)
	var ambiguous AmbiguousSessionError
	switch {
	case errors.Is(err, platforms.ErrNotFound):
		return mcplib.NewToolResultError("session not found")
	case errors.As(err, &ambiguous):
		return mcplib.NewToolResultError(ambiguous.Error())
	case errors.Is(err, ErrArchiveBusyRemote):
		return mcplib.NewToolResultError(ErrArchiveBusyRemote.Error())
	case err != nil:
		return mcplib.NewToolResultError("session request failed")
	}
	return toolResultJSON(result)
}
