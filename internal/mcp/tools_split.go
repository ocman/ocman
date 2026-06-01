package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/worktree"
)

// splitSessionReader is the subset of db.DB needed by the split tools.
type splitSessionReader interface {
	GetSession(sessionID string) (*db.Session, error)
	GetSessionMessages(sessionID string) ([]db.Message, error)
}

// splitTools holds the dependencies for the split_to_session and
// split_to_worktree tool handlers.
type splitTools struct {
	composer *PromptComposer
	launcher *SessionLauncher
	platform string // platform ID, e.g. "opencode"
}

// splitToSessionTool returns the mcp-go tool definition for split_to_session.
func splitToSessionTool() mcplib.Tool {
	return mcplib.NewTool("split_to_session",
		mcplib.WithDescription("Launch a child OpenCode session in the same working directory."),
		mcplib.WithString("session_id",
			mcplib.Required(),
			mcplib.Description("Parent session ID."),
		),
		mcplib.WithString("intent",
			mcplib.Required(),
			mcplib.Description("Sub-task for the child session."),
		),
		mcplib.WithObject("context_options",
			mcplib.Description("Prompt context toggles. Defaults true."),
			mcplib.Properties(map[string]interface{}{
				"recent_messages":  map[string]interface{}{"type": "boolean"},
				"relevant_files":   map[string]interface{}{"type": "boolean"},
				"git_branch":       map[string]interface{}{"type": "boolean"},
				"git_diff_stat":    map[string]interface{}{"type": "boolean"},
				"project_metadata": map[string]interface{}{"type": "boolean"},
			}),
		),
	)
}

// splitToWorktreeTool returns the mcp-go tool definition for split_to_worktree.
func splitToWorktreeTool() mcplib.Tool {
	return mcplib.NewTool("split_to_worktree",
		mcplib.WithDescription("Launch a child OpenCode session in a fresh git worktree."),
		mcplib.WithString("session_id",
			mcplib.Required(),
			mcplib.Description("Parent session ID."),
		),
		mcplib.WithString("intent",
			mcplib.Required(),
			mcplib.Description("Sub-task for the child session."),
		),
		mcplib.WithString("branch",
			mcplib.Required(),
			mcplib.Description("Branch name for the worktree."),
		),
		mcplib.WithString("base_ref",
			mcplib.Description("Base ref. Defaults to repo default branch."),
		),
		mcplib.WithObject("context_options",
			mcplib.Description("Prompt context toggles. Defaults true."),
			mcplib.Properties(map[string]interface{}{
				"recent_messages":  map[string]interface{}{"type": "boolean"},
				"relevant_files":   map[string]interface{}{"type": "boolean"},
				"git_branch":       map[string]interface{}{"type": "boolean"},
				"git_diff_stat":    map[string]interface{}{"type": "boolean"},
				"project_metadata": map[string]interface{}{"type": "boolean"},
			}),
		),
	)
}

// addSplitTools registers the split tools on the MCP server.
func addSplitTools(s *server.MCPServer, t *splitTools) {
	s.AddTool(splitToSessionTool(), t.handleSplitToSession)
	s.AddTool(splitToWorktreeTool(), t.handleSplitToWorktree)
}

// handleSplitToSession handles the split_to_session tool call.
func (t *splitTools) handleSplitToSession(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	sessionID, err := req.RequireString("session_id")
	if err != nil {
		return mcplib.NewToolResultError("session_id is required"), nil
	}
	intent, err := req.RequireString("intent")
	if err != nil {
		return mcplib.NewToolResultError("intent is required"), nil
	}

	opts := parseContextOptions(req)

	// Compose the enriched prompt.
	prompt, err := t.composer.Compose(ctx, sessionID, intent, opts)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("composing prompt: %v", err)), nil
	}

	// Look up the parent session's directory.
	session, err := t.composer.db.GetSession(sessionID)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("session not found: %v", err)), nil
	}

	// Launch the child session.
	childID, err := t.launcher.Launch(ctx, LaunchRequest{
		ParentSessionID: sessionID,
		Platform:        t.platform,
		Directory:       session.Directory,
		Intent:          intent,
		ComposedPrompt:  prompt,
	})
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("launching child session: %v", err)), nil
	}

	result := map[string]interface{}{
		"child_session_id": childID,
		"status":           "starting",
		"intent":           intent,
	}
	return toolResultJSON(result), nil
}

// handleSplitToWorktree handles the split_to_worktree tool call.
func (t *splitTools) handleSplitToWorktree(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	sessionID, err := req.RequireString("session_id")
	if err != nil {
		return mcplib.NewToolResultError("session_id is required"), nil
	}
	intent, err := req.RequireString("intent")
	if err != nil {
		return mcplib.NewToolResultError("intent is required"), nil
	}
	branch, err := req.RequireString("branch")
	if err != nil {
		return mcplib.NewToolResultError("branch is required"), nil
	}
	baseRef := req.GetString("base_ref", "")

	opts := parseContextOptions(req)

	// Compose the enriched prompt.
	prompt, err := t.composer.Compose(ctx, sessionID, intent, opts)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("composing prompt: %v", err)), nil
	}

	// Look up the parent session's directory to find the repo root.
	session, err := t.composer.db.GetSession(sessionID)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("session not found: %v", err)), nil
	}

	repoRoot, err := worktree.ResolveRepoRoot(ctx, session.Directory)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("resolving repo root: %v", err)), nil
	}

	// If no base_ref provided, resolve the default.
	if baseRef == "" {
		baseRef = worktree.ResolveBaseRef(ctx, repoRoot)
	}

	childID, wtResult, err := t.launcher.LaunchWithWorktree(
		ctx,
		LaunchRequest{
			ParentSessionID: sessionID,
			Platform:        t.platform,
			Intent:          intent,
			ComposedPrompt:  prompt,
		},
		worktree.CreateRequest{
			RepoRoot:  repoRoot,
			Branch:    branch,
			NewBranch: true,
			BaseRef:   baseRef,
		},
	)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("launching worktree session: %v", err)), nil
	}

	result := map[string]interface{}{
		"child_session_id": childID,
		"worktree_path":    wtResult.Path,
		"branch":           wtResult.Branch,
		"status":           "starting",
		"intent":           intent,
	}
	return toolResultJSON(result), nil
}

// parseContextOptions extracts context_options from the tool request.
// Missing or null fields default to true (all sources enabled).
func parseContextOptions(req mcplib.CallToolRequest) ContextOptions {
	opts := DefaultContextOptions()
	args := req.GetArguments()
	raw, ok := args["context_options"]
	if !ok || raw == nil {
		return opts
	}
	// Re-marshal and unmarshal to get a typed map.
	b, err := json.Marshal(raw)
	if err != nil {
		return opts
	}
	var co struct {
		RecentMessages *bool `json:"recent_messages"`
		RelevantFiles  *bool `json:"relevant_files"`
		GitBranch      *bool `json:"git_branch"`
		GitDiffStat    *bool `json:"git_diff_stat"`
		ProjectMeta    *bool `json:"project_metadata"`
	}
	if err := json.Unmarshal(b, &co); err != nil {
		return opts
	}
	if co.RecentMessages != nil {
		opts.RecentMessages = *co.RecentMessages
	}
	if co.RelevantFiles != nil {
		opts.RelevantFiles = *co.RelevantFiles
	}
	if co.GitBranch != nil {
		opts.GitBranch = *co.GitBranch
	}
	if co.GitDiffStat != nil {
		opts.GitDiffStat = *co.GitDiffStat
	}
	if co.ProjectMeta != nil {
		opts.ProjectMeta = *co.ProjectMeta
	}
	return opts
}

// toolResultJSON marshals v to JSON and returns a text tool result.
// Falls back to a plain string representation on marshal error.
func toolResultJSON(v interface{}) *mcplib.CallToolResult {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcplib.NewToolResultText(fmt.Sprintf("%v", v))
	}
	return mcplib.NewToolResultText(string(b))
}
