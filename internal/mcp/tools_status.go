package mcp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os/exec"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/state"
)

// statusSessionReader is the subset of db.DB needed by the status tools
// to infer child session completion from OpenCode's database.
type statusSessionReader interface {
	GetSessions(directory string, since int64) ([]db.Session, error)
}

// childSessionDB is the subset of state.DB needed by the status tools.
type childSessionDB interface {
	GetChildSession(id string) (*state.ChildSession, error)
	ListChildSessionsByParent(parentSessionID string) ([]state.ChildSession, error)
	CancelChildSession(id string, cancelledAt int64) error
}

// statusTools holds the dependencies for the status/management tool handlers.
type statusTools struct {
	stateDB  childSessionDB
	ocDB     statusSessionReader // may be nil when OpenCode DB is unavailable
}

// getSessionStatusTool returns the tool definition for get_session_status.
func getSessionStatusTool() mcplib.Tool {
	return mcplib.NewTool("get_session_status",
		mcplib.WithDescription("Get the current status of a child session previously spawned by split_to_session or split_to_worktree."),
		mcplib.WithString("child_session_id",
			mcplib.Required(),
			mcplib.Description("The child session ID returned by split_to_session or split_to_worktree."),
		),
	)
}

// listChildSessionsTool returns the tool definition for list_child_sessions.
func listChildSessionsTool() mcplib.Tool {
	return mcplib.NewTool("list_child_sessions",
		mcplib.WithDescription("List all sessions spawned from a given parent session, with their current status."),
		mcplib.WithString("session_id",
			mcplib.Required(),
			mcplib.Description("The parent session ID."),
		),
	)
}

// cancelSessionTool returns the tool definition for cancel_session.
func cancelSessionTool() mcplib.Tool {
	return mcplib.NewTool("cancel_session",
		mcplib.WithDescription("Cancel a running child session by terminating its tmux window. Idempotent: cancelling an already-terminal session is a no-op."),
		mcplib.WithString("child_session_id",
			mcplib.Required(),
			mcplib.Description("The child session ID to cancel."),
		),
	)
}

// addStatusTools registers the status/management tools on the MCP server.
func addStatusTools(s *server.MCPServer, t *statusTools) {
	s.AddTool(getSessionStatusTool(), t.handleGetSessionStatus)
	s.AddTool(listChildSessionsTool(), t.handleListChildSessions)
	s.AddTool(cancelSessionTool(), t.handleCancelSession)
}

// handleGetSessionStatus handles the get_session_status tool call.
func (t *statusTools) handleGetSessionStatus(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	childID, err := req.RequireString("child_session_id")
	if err != nil {
		return mcplib.NewToolResultError("child_session_id is required"), nil
	}

	cs, err := t.stateDB.GetChildSession(childID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return mcplib.NewToolResultError(fmt.Sprintf("child session not found: %s", childID)), nil
		}
		return mcplib.NewToolResultError(fmt.Sprintf("looking up child session: %v", err)), nil
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

// handleCancelSession handles the cancel_session tool call.
func (t *statusTools) handleCancelSession(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	childID, err := req.RequireString("child_session_id")
	if err != nil {
		return mcplib.NewToolResultError("child_session_id is required"), nil
	}

	cs, err := t.stateDB.GetChildSession(childID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return mcplib.NewToolResultError(fmt.Sprintf("child session not found: %s", childID)), nil
		}
		return mcplib.NewToolResultError(fmt.Sprintf("looking up child session: %v", err)), nil
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
		if err := killTmuxTarget(cs.TmuxTarget); err != nil {
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

// killTmuxTarget kills a tmux window or session by target identifier.
// For a "session:window" target it uses kill-window; for a plain session
// name it uses kill-session. Errors are non-fatal (the window may already
// be gone).
func killTmuxTarget(target string) error {
	var args []string
	if containsColon(target) {
		args = []string{"kill-window", "-t", target}
	} else {
		args = []string{"kill-session", "-t", target}
	}
	out, err := exec.Command("tmux", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux %v: %w: %s", args, err, string(out))
	}
	return nil
}

// containsColon reports whether s contains a colon character.
func containsColon(s string) bool {
	for _, c := range s {
		if c == ':' {
			return true
		}
	}
	return false
}
