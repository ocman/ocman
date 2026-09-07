package mcp

import (
	"context"
	"errors"
	"strings"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type sessionService interface {
	ListSessions(context.Context, string) ([]db.Session, error)
	GetSession(context.Context, string, string, int) (*platforms.SessionDetail, error)
}

type sessionTools struct{ svc sessionService }

type sessionAction struct {
	name, description, example string
	required, optional         []string
	output                     string
}

var sessionActions = []sessionAction{
	{name: "help", description: "Describes every available read-only session action.", example: `{"action":"help"}`, output: "Session action documentation"},
	{name: "list", description: "Lists recent sessions, optionally scoped to one directory.", example: `{"action":"list","directory":"/repo","limit":50}`, optional: []string{"directory", "limit"}, output: "Session[]"},
	{name: "search", description: "Searches recent session IDs, titles, directories, platforms, and host names.", example: `{"action":"search","query":"review","limit":20}`, required: []string{"query"}, optional: []string{"directory", "limit"}, output: "Session[]"},
	{name: "get", description: "Gets one session with its latest messages and parts.", example: `{"action":"get","platform":"opencode","session_id":"ses_1","message_limit":20}`, required: []string{"platform", "session_id"}, optional: []string{"message_limit"}, output: "SessionDetail"},
}

func sessionServerTools(tools *sessionTools) []server.ServerTool {
	if tools == nil || tools.svc == nil {
		return nil
	}
	return []server.ServerTool{{Tool: mcplib.NewTool("sessions",
		mcplib.WithDescription("Read-only session inspection and search. Use action help for schemas and examples."),
		mcplib.WithString("action", mcplib.Required()), mcplib.WithString("session_id"), mcplib.WithString("platform"),
		mcplib.WithString("directory"), mcplib.WithString("query"), mcplib.WithNumber("limit"), mcplib.WithNumber("message_limit")), Handler: tools.handle}}
}

func addSessionTools(s *server.MCPServer, tools *sessionTools) {
	for _, tool := range sessionServerTools(tools) {
		s.AddTool(tool.Tool, tool.Handler)
	}
}

func (t *sessionTools) handle(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	action, err := req.RequireString("action")
	if err != nil {
		return mcplib.NewToolResultError("action is required"), nil
	}
	registered, ok := sessionActionFor(action)
	if !ok {
		return mcplib.NewToolResultError("unknown action"), nil
	}
	arguments, ok := req.Params.Arguments.(map[string]any)
	for _, field := range registered.required {
		if value, present := arguments[field]; !ok || !present || value == nil || value == "" {
			return mcplib.NewToolResultError(field + " is required"), nil
		}
	}

	switch action {
	case "help":
		return toolResultJSON(sessionHelp()), nil
	case "list", "search":
		limit := req.GetInt("limit", 100)
		if limit < 1 || limit > 500 {
			return mcplib.NewToolResultError("limit must be between 1 and 500"), nil
		}
		query := strings.TrimSpace(req.GetString("query", ""))
		if action == "search" && query == "" {
			return mcplib.NewToolResultError("query is required"), nil
		}
		sessions, err := t.svc.ListSessions(ctx, req.GetString("directory", ""))
		if err != nil {
			return mcplib.NewToolResultError("session request failed"), nil
		}
		if action == "search" {
			sessions = searchSessions(sessions, query)
		}
		if len(sessions) > limit {
			sessions = sessions[:limit]
		}
		if sessions == nil {
			sessions = []db.Session{}
		}
		return toolResultJSON(sessions), nil
	case "get":
		limit := req.GetInt("message_limit", 20)
		if limit < 0 || limit > 100 {
			return mcplib.NewToolResultError("message_limit must be between 0 and 100"), nil
		}
		detail, err := t.svc.GetSession(ctx, req.GetString("platform", ""), req.GetString("session_id", ""), limit)
		if errors.Is(err, platforms.ErrNotFound) {
			return mcplib.NewToolResultError("session not found"), nil
		}
		if err != nil {
			return mcplib.NewToolResultError("session request failed"), nil
		}
		return toolResultJSON(detail), nil
	}
	return mcplib.NewToolResultError("unknown action"), nil
}

func sessionActionFor(name string) (sessionAction, bool) {
	for _, action := range sessionActions {
		if action.name == name {
			return action, true
		}
	}
	return sessionAction{}, false
}

func sessionHelp() map[string]any {
	help := make(map[string]any, len(sessionActions)+3)
	actions := make([]string, 0, len(sessionActions))
	for _, action := range sessionActions {
		actions = append(actions, action.name)
		help[action.name] = map[string]any{"required": append([]string{}, action.required...), "optional": append([]string{}, action.optional...), "action": action.description, "example": action.example, "output_schema": action.output}
	}
	help["actions"] = actions
	help["rules"] = []string{"all actions are read-only", "list and search return at most 500 recent sessions", "message_limit is between 0 and 100; 0 returns metadata without messages"}
	help["errors"] = []string{"action is required", "unknown action", "session not found", "session request failed"}
	return help
}

func searchSessions(sessions []db.Session, query string) []db.Session {
	query = strings.ToLower(strings.TrimSpace(query))
	result := make([]db.Session, 0)
	for _, session := range sessions {
		if strings.Contains(strings.ToLower(strings.Join([]string{session.ID, session.Title, session.Directory, session.Platform, session.RemoteName}, "\n")), query) {
			result = append(result, session)
		}
	}
	return result
}
