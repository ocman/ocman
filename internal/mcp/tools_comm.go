package mcp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

// commChildSessionDB is the subset of state.DB needed by the communication tools.
type commChildSessionDB interface {
	GetChildSession(id string) (*state.ChildSession, error)
}

type childSessionReopener interface {
	ReopenChildSession(id string) error
}

// commTools holds dependencies for parent <-> child messaging.
type commTools struct {
	stateDB  commChildSessionDB
	platform platformAdapter
}

// sendMessageToChildTool returns the tool definition for send_message_to_child.
func sendMessageToChildTool() mcplib.Tool {
	return mcplib.NewTool("send_message_to_child",
		mcplib.WithDescription("Send a message to a child session."),
		mcplib.WithString("session_id",
			mcplib.Required(),
			mcplib.Description("Parent session ID."),
		),
		mcplib.WithString("child_session_id",
			mcplib.Required(),
			mcplib.Description("Child session ID."),
		),
		mcplib.WithString("message",
			mcplib.Required(),
			mcplib.Description("Message."),
		),
	)
}

// sendMessageToParentTool returns the tool definition for send_message_to_parent.
func sendMessageToParentTool() mcplib.Tool {
	return mcplib.NewTool("send_message_to_parent",
		mcplib.WithDescription("Send a message to the parent session."),
		mcplib.WithString("child_session_id",
			mcplib.Required(),
			mcplib.Description("Child session ID."),
		),
		mcplib.WithString("message",
			mcplib.Required(),
			mcplib.Description("Message."),
		),
	)
}

// addCommTools registers parent <-> child messaging tools on the MCP server.
func addCommTools(s *server.MCPServer, t *commTools) {
	s.AddTool(sendMessageToChildTool(), t.handleSendMessageToChild)
	s.AddTool(sendMessageToParentTool(), t.handleSendMessageToParent)
}

// handleSendMessageToChild handles the send_message_to_child tool call.
func (t *commTools) handleSendMessageToChild(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	parentID, err := req.RequireString("session_id")
	if err != nil {
		return mcplib.NewToolResultError("session_id is required"), nil
	}
	childID, err := req.RequireString("child_session_id")
	if err != nil {
		return mcplib.NewToolResultError("child_session_id is required"), nil
	}
	message, err := req.RequireString("message")
	if err != nil || strings.TrimSpace(message) == "" {
		return mcplib.NewToolResultError("message is required"), nil
	}

	cs, toolErr := lookupChildSession(t.stateDB, childID)
	if toolErr != nil {
		return toolErr, nil
	}
	if cs.ParentSessionID != parentID {
		return mcplib.NewToolResultError(fmt.Sprintf("child session %s does not belong to parent session %s", childID, parentID)), nil
	}

	delivered := fmt.Sprintf("Message from parent session %s:\n\n%s", parentID, message)
	if err := t.sendMessage(ctx, childID, delivered); err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("sending message to child: %v", err)), nil
	}
	reopener, ok := t.stateDB.(childSessionReopener)
	if !ok {
		return mcplib.NewToolResultError("reopening child session: state database does not support reopening"), nil
	}
	if err := reopener.ReopenChildSession(childID); err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("reopening child session: %v", err)), nil
	}

	return toolResultJSON(map[string]interface{}{
		"delivered":        true,
		"to_session_id":    childID,
		"from_session_id":  parentID,
		"relationship":     "parent_to_child",
		"child_session_id": childID,
	}), nil
}

// handleSendMessageToParent handles the send_message_to_parent tool call.
func (t *commTools) handleSendMessageToParent(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	childID, err := req.RequireString("child_session_id")
	if err != nil {
		return mcplib.NewToolResultError("child_session_id is required"), nil
	}
	message, err := req.RequireString("message")
	if err != nil || strings.TrimSpace(message) == "" {
		return mcplib.NewToolResultError("message is required"), nil
	}

	cs, toolErr := lookupChildSession(t.stateDB, childID)
	if toolErr != nil {
		return toolErr, nil
	}
	if cs.ParentSessionID == "" {
		return mcplib.NewToolResultError(fmt.Sprintf("child session %s has no parent session", childID)), nil
	}

	delivered := fmt.Sprintf("Message from child session %s (%s):\n\n%s", childID, cs.Intent, message)
	if err := t.sendMessage(ctx, cs.ParentSessionID, delivered); err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("sending message to parent: %v", err)), nil
	}

	return toolResultJSON(map[string]interface{}{
		"delivered":        true,
		"to_session_id":    cs.ParentSessionID,
		"from_session_id":  childID,
		"relationship":     "child_to_parent",
		"child_session_id": childID,
	}), nil
}

// lookupChildSession fetches a child session by ID, mapping not-found
// and generic lookup failures to MCP tool errors. The second return
// value is non-nil on failure. Shared by the comm and status tools so
// the error wording can't drift between handlers.
func lookupChildSession(db commChildSessionDB, childID string) (*state.ChildSession, *mcplib.CallToolResult) {
	cs, err := db.GetChildSession(childID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, mcplib.NewToolResultError(fmt.Sprintf("child session not found: %s", childID))
		}
		return nil, mcplib.NewToolResultError(fmt.Sprintf("looking up child session: %v", err))
	}
	return cs, nil
}

func (t *commTools) sendMessage(ctx context.Context, sessionID, message string) error {
	if t.platform == nil {
		return fmt.Errorf("platform adapter unavailable")
	}
	return t.platform.SendMessage(ctx, platforms.SendMessageRequest{
		SessionID: sessionID,
		Message:   message,
	})
}
