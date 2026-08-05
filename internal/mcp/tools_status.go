package mcp

import (
	"context"
	"errors"
	"fmt"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/tmux"
)

// statusSessionReader is the subset of db.DB needed by the status tools
// to inspect OpenCode sessions.
type statusSessionReader interface {
	GetSessions(directory string, since int64) ([]db.Session, error)
	GetSession(id string) (*db.Session, error)
	FindRunningToolSessionID(tool, directory string) (string, error)
}

// childSessionDB is the subset of state.DB needed by the status tools.
type childSessionDB interface {
	GetChildSession(id string) (*state.ChildSession, error)
	ListChildSessionsByParent(parentSessionID string) ([]state.ChildSession, error)
	CancelChildSession(id string, cancelledAt int64) error
}

// statusTools holds the dependencies for the status/management tool handlers.
type statusTools struct {
	stateDB childSessionDB
	ocDB    statusSessionReader // may be nil when OpenCode DB is unavailable
}

// getSessionStatusTool returns the tool definition for get_session_status.
func getSessionStatusTool() mcplib.Tool {
	return mcplib.NewTool("get_session_status",
		mcplib.WithDescription("Get child session status."),
		mcplib.WithString("child_session_id",
			mcplib.Required(),
			mcplib.Description("Child session ID."),
		),
	)
}

// listChildSessionsTool returns the tool definition for list_child_sessions.
func listChildSessionsTool() mcplib.Tool {
	return mcplib.NewTool("list_child_sessions",
		mcplib.WithDescription("List child sessions for a parent."),
		mcplib.WithString("session_id",
			mcplib.Required(),
			mcplib.Description("Parent session ID."),
		),
	)
}

// getCurrentSessionIDTool returns the tool definition for get_current_session_id.
func getCurrentSessionIDTool() mcplib.Tool {
	return mcplib.NewTool("get_current_session_id",
		mcplib.WithDescription("Return the most recent OpenCode session ID."),
		mcplib.WithString("directory",
			mcplib.Description("Optional project directory filter."),
		),
	)
}

// cancelSessionTool returns the tool definition for cancel_session.
func cancelSessionTool() mcplib.Tool {
	return mcplib.NewTool("cancel_session",
		mcplib.WithDescription("Cancel a running child session."),
		mcplib.WithString("child_session_id",
			mcplib.Required(),
			mcplib.Description("Child session ID."),
		),
	)
}

// addStatusTools registers the status/management tools on the MCP server.
func addStatusTools(s *server.MCPServer, t *statusTools) {
	s.AddTool(getSessionStatusTool(), t.handleGetSessionStatus)
	s.AddTool(getCurrentSessionIDTool(), t.handleGetCurrentSessionID)
	s.AddTool(listChildSessionsTool(), t.handleListChildSessions)
	s.AddTool(cancelSessionTool(), t.handleCancelSession)
}

// handleGetSessionStatus handles the get_session_status tool call.
func (t *statusTools) handleGetSessionStatus(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	childID, err := req.RequireString("child_session_id")
	if err != nil {
		return mcplib.NewToolResultError("child_session_id is required"), nil
	}

	cs, toolErr := lookupChildSession(t.stateDB, childID)
	if toolErr != nil {
		return toolErr, nil
	}

	result := map[string]interface{}{
		"child_session_id": cs.ID,
		"status":           cs.Status,
		"intent":           cs.Intent,
	}
	if cs.Summary != "" {
		result["summary"] = cs.Summary
	}
	if cs.WorktreePath != "" {
		result["worktree_path"] = cs.WorktreePath
	}
	if cs.CompletedAt > 0 {
		result["completed_at"] = cs.CompletedAt
	}

	return toolResultJSON(result), nil
}

// handleListChildSessions handles the list_child_sessions tool call.
func (t *statusTools) handleListChildSessions(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	sessionID, err := req.RequireString("session_id")
	if err != nil {
		return mcplib.NewToolResultError("session_id is required"), nil
	}

	children, err := t.stateDB.ListChildSessionsByParent(sessionID)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("listing child sessions: %v", err)), nil
	}

	type childEntry struct {
		ChildSessionID string `json:"child_session_id"`
		Intent         string `json:"intent"`
		Status         string `json:"status"`
		CreatedAt      int64  `json:"created_at"`
		WorktreePath   string `json:"worktree_path,omitempty"`
		Branch         string `json:"branch,omitempty"`
	}

	entries := make([]childEntry, 0, len(children))
	for _, cs := range children {
		entries = append(entries, childEntry{
			ChildSessionID: cs.ID,
			Intent:         cs.Intent,
			Status:         cs.Status,
			CreatedAt:      cs.CreatedAt,
			WorktreePath:   cs.WorktreePath,
			Branch:         cs.Branch,
		})
	}

	return toolResultJSON(entries), nil
}

// handleGetCurrentSessionID handles the get_current_session_id tool call.
func (t *statusTools) handleGetCurrentSessionID(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	if t.ocDB == nil {
		return mcplib.NewToolResultError("OpenCode database is unavailable"), nil
	}

	directory := req.GetString("directory", "")
	callerID, callerErr := t.ocDB.FindRunningToolSessionID("get_current_session_id", directory)
	if errors.Is(callerErr, db.ErrAmbiguousRunningTool) {
		return mcplib.NewToolResultError("multiple sessions are requesting their ID; retry the call"), nil
	}
	if callerErr != nil {
		log.WithError(callerErr).Warn("failed to identify session invoking get_current_session_id")
	}
	if callerID != "" {
		s, err := t.ocDB.GetSession(callerID)
		if err == nil {
			return currentSessionResult(*s, "calling_session"), nil
		}
		log.WithError(err).Warn("failed to load session invoking get_current_session_id")
	}

	sessions, err := t.ocDB.GetSessions(directory, 0)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("looking up current session: %v", err)), nil
	}
	// GetSessions returns raw rows; drop finished subagents so the
	// most-recent fallback below can't hand back a Task-tool session.
	sessions = db.FilterInactiveChildren(sessions)
	if len(sessions) == 0 {
		if directory != "" {
			return mcplib.NewToolResultError(fmt.Sprintf("no sessions found for directory: %s", directory)), nil
		}
		return mcplib.NewToolResultError("no sessions found"), nil
	}

	return currentSessionResult(sessions[0], "most_recent_session"), nil
}

func currentSessionResult(s db.Session, selectionMode string) *mcplib.CallToolResult {
	return toolResultJSON(map[string]interface{}{
		"session_id":     s.ID,
		"directory":      s.Directory,
		"title":          s.Title,
		"time_updated":   s.TimeUpdated,
		"selection_mode": selectionMode,
	})
}

// handleCancelSession handles the cancel_session tool call.
func (t *statusTools) handleCancelSession(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	childID, err := req.RequireString("child_session_id")
	if err != nil {
		return mcplib.NewToolResultError("child_session_id is required"), nil
	}

	cs, toolErr := lookupChildSession(t.stateDB, childID)
	if toolErr != nil {
		return toolErr, nil
	}

	// If already in a terminal state, treat as idempotent success.
	if isTerminalStatus(cs.Status) {
		result := map[string]interface{}{
			"success": true,
			"message": fmt.Sprintf("session is already in terminal state: %s", cs.Status),
		}
		return toolResultJSON(result), nil
	}

	// Kill the tmux window/session if we have a target.
	if cs.TmuxTarget != "" {
		if err := tmux.KillTarget(cs.TmuxTarget); err != nil {
			log.WithFields(log.Fields{
				"childSessionID": childID,
				"tmuxTarget":     cs.TmuxTarget,
				"error":          err,
			}).Warn("mcp: cancel_session: tmux kill failed (continuing)")
			// Don't fail: update state.db regardless so the session
			// doesn't get stuck in a non-terminal state.
		}
	}

	if err := t.stateDB.CancelChildSession(childID, time.Now().UnixMilli()); err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("cancelling child session: %v", err)), nil
	}

	result := map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("session %s cancelled", childID),
	}
	return toolResultJSON(result), nil
}

// isTerminalStatus reports whether a status string is a terminal state.
func isTerminalStatus(status string) bool {
	switch status {
	case "completed", "error", "cancelled":
		return true
	}
	return false
}
