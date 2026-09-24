package mcp

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type sessionService interface {
	ListSessions(context.Context, string) ([]db.Session, error)
	GetSession(context.Context, string, string, int) (*platforms.SessionDetail, error)
	// SearchSessionText scans message text of sessions updated since the
	// unix-ms cutoff. Slow: it reads every text part in the window.
	SearchSessionText(ctx context.Context, query, directory string, since int64) ([]db.TextMatch, error)
}

// sessionSearchResult is a Session plus the content hits that matched it.
type sessionSearchResult struct {
	db.Session
	Matches []db.TextMatch `json:"matches,omitempty"`
}

const maxMatchesPerSession = 5

// AmbiguousSessionError reports a platform-less lookup that matched a session
// ID on more than one platform; the caller must pass one of Platforms.
type AmbiguousSessionError struct{ Platforms []string }

func (e AmbiguousSessionError) Error() string {
	return "session_id exists on multiple platforms (" + strings.Join(e.Platforms, ", ") + "); pass platform"
}

type sessionTools struct{ svc sessionService }

type sessionAction struct {
	name, description, example string
	required, optional         []string
	output                     string
}

var sessionActions = []sessionAction{
	{name: "help", description: "Describes every available session action.", example: `{"action":"help"}`, output: "Session action documentation"},
	{name: "list", description: "Lists recent sessions, optionally scoped to one directory.", example: `{"action":"list","directory":"/repo","limit":50}`, optional: []string{"directory", "limit"}, output: "Session[]"},
	{name: "search", description: "Searches recent session IDs, titles, directories, platforms, and host names. With content true, also searches user and assistant message text (not tool output) of sessions updated in the last since_days; this is slow (tens of seconds).", example: `{"action":"search","query":"weave-cli","content":true,"since_days":7}`, required: []string{"query"}, optional: []string{"directory", "limit", "content", "since_days"}, output: "(Session & {matches?: {partId, messageId, role, snippet}[]})[]"},
	{name: "get", description: "Gets one session with its latest messages and parts. Without platform, every platform is searched.", example: `{"action":"get","session_id":"ses_1","message_limit":20}`, required: []string{"session_id"}, optional: []string{"platform", "message_limit"}, output: "SessionDetail"},
	{name: "create", description: "Starts a new top-level session and sends it prompt. Pass your own platform and session_id to default directory to your project root and run on your machine, or pass an absolute directory.", example: `{"action":"create","prompt":"Review the open PR","model":"anthropic/claude-sonnet-4","agent":"plan","platform":"opencode","session_id":"ses_caller"}`, required: []string{"prompt"}, optional: []string{"model", "agent", "title", "directory", "platform", "session_id"}, output: "{platform, session_id, directory}"},
}

func sessionServerTools(tools *sessionTools) []server.ServerTool {
	if tools == nil || tools.svc == nil {
		return nil
	}
	return []server.ServerTool{{Tool: mcplib.NewTool("sessions",
		mcplib.WithDescription("Inspect, search, and create sessions (actions: help, list, search, get, create). Use action help for schemas and examples."),
		mcplib.WithString("action", mcplib.Required()), mcplib.WithString("session_id"), mcplib.WithString("platform"),
		mcplib.WithString("directory"), mcplib.WithString("query"), mcplib.WithNumber("limit"), mcplib.WithNumber("message_limit"),
		mcplib.WithString("prompt"), mcplib.WithString("model", mcplib.Description("provider/model")), mcplib.WithString("agent"), mcplib.WithString("title"),
		mcplib.WithBoolean("content"), mcplib.WithNumber("since_days")), Handler: tools.handle}}
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
		sinceDays := req.GetInt("since_days", 7)
		if sinceDays < 1 || sinceDays > 365 {
			return mcplib.NewToolResultError("since_days must be between 1 and 365"), nil
		}
		directory := req.GetString("directory", "")
		sessions, err := t.svc.ListSessions(ctx, directory)
		if err != nil {
			return mcplib.NewToolResultError("session request failed"), nil
		}
		if action == "search" && req.GetBool("content", false) {
			since := time.Now().AddDate(0, 0, -sinceDays).UnixMilli()
			matches, err := t.svc.SearchSessionText(ctx, query, directory, since)
			if err != nil {
				return mcplib.NewToolResultError("session request failed"), nil
			}
			results := searchSessionsWithContent(sessions, query, matches)
			if len(results) > limit {
				results = results[:limit]
			}
			return toolResultJSON(results), nil
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
		var ambiguous AmbiguousSessionError
		if errors.As(err, &ambiguous) {
			return mcplib.NewToolResultError(ambiguous.Error()), nil
		}
		if err != nil {
			return mcplib.NewToolResultError("session request failed"), nil
		}
		return toolResultJSON(detail), nil
	case "create":
		return t.create(ctx, req), nil
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
	help["rules"] = []string{"only create changes state; start a session only when the user asks for one", "list and search return at most 500 recent sessions", "message_limit is between 0 and 100; 0 returns metadata without messages", "get: platform is optional; pass it only when the session_id exists on multiple platforms", "create: model is provider/model; directory is absolute; platform and session_id identify the calling session and go together", "create: the new session uses the platform's default permissions", "search content: opt-in and slow; since_days is between 1 and 365 (default 7); only this machine's sessions are content-searched; at most 5 matches per session"}
	help["errors"] = []string{"action is required", "unknown action", "session not found", "session_id exists on multiple platforms (<platforms>); pass platform", "session request failed", "prompt is required", "model must be provider/model", "directory must be absolute", "platform and session_id must be provided together", "invalid session request: <reason>", "session <id> was created on <platform> but the prompt failed", "since_days must be between 1 and 365"}
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

// searchSessionsWithContent keeps sessions that match on metadata or have
// content hits, in listing order, attaching up to maxMatchesPerSession hits.
// ponytail: a hit on a session outside the 500-session listing is dropped.
func searchSessionsWithContent(sessions []db.Session, query string, matches []db.TextMatch) []sessionSearchResult {
	byKey := map[string][]db.TextMatch{}
	for _, m := range matches {
		key := m.Platform + "\x00" + m.SessionID
		if len(byKey[key]) < maxMatchesPerSession {
			byKey[key] = append(byKey[key], m)
		}
	}
	metadata := map[string]bool{}
	for _, s := range searchSessions(sessions, query) {
		metadata[s.Platform+"\x00"+s.ID] = true
	}
	results := make([]sessionSearchResult, 0)
	for _, s := range sessions {
		key := s.Platform + "\x00" + s.ID
		if hits := byKey[key]; metadata[key] || len(hits) > 0 {
			results = append(results, sessionSearchResult{Session: s, Matches: hits})
		}
	}
	return results
}
