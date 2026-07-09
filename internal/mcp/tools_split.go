package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/permissions"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

// permissionInheriter is the slice of *state.DB the split tools need to
// inherit the parent's always-allow permissions into a child (issue
// #101). Nil disables inheritance (no state DB).
type permissionInheriter interface {
	GetWorktreeInheritPermissions() (bool, error)
	ListApprovedPermissions(platform, sessionID string) ([]state.ApprovedPermission, error)
}

// splitTools holds the dependencies for the new_session tool handler.
type splitTools struct {
	composer *PromptComposer
	launcher *SessionLauncher
	platform string // platform ID, e.g. "opencode"
	// inherit provides the parent's approved permissions and the
	// inherit-on/off setting. Nil = inheritance disabled.
	inherit permissionInheriter
}

// inheritedRules builds the parent's always-allow ruleset for a child
// launch when the setting is on. Soft-fail: returns (nil, 0, note) on
// any error so the caller can proceed with the launch and surface the
// note. When inheritance is off or yields nothing, returns
// (nil, 0, "").
func (t *splitTools) inheritedRules(parentSessionID string) (rules []platforms.PermissionRule, count int, errNote string) {
	if t.inherit == nil || parentSessionID == "" {
		return nil, 0, ""
	}
	on, err := t.inherit.GetWorktreeInheritPermissions()
	if err != nil {
		log.WithError(err).Warn("mcp: reading worktree inherit-permissions setting")
		return nil, 0, "reading setting: " + err.Error()
	}
	if !on {
		return nil, 0, ""
	}
	rules, count, err = permissions.BuildInheritedRules(t.inherit, t.platform, parentSessionID)
	if err != nil {
		log.WithError(err).Warn("mcp: building inherited permission rules")
		return nil, 0, "building rules: " + err.Error()
	}
	return rules, count, ""
}

// mergeInheritedRules orders inherited rules first and caller-supplied
// rules last so a caller-supplied rule wins on conflict — OpenCode
// evaluates the last matching rule, so trailing rules take precedence.
func mergeInheritedRules(caller, inherited []platforms.PermissionRule) []platforms.PermissionRule {
	if len(inherited) == 0 {
		return caller
	}
	merged := make([]platforms.PermissionRule, 0, len(inherited)+len(caller))
	merged = append(merged, inherited...)
	merged = append(merged, caller...)
	return merged
}

// newSessionTool returns the mcp-go tool definition for new_session.
func newSessionTool() mcplib.Tool {
	return mcplib.NewTool("new_session",
		mcplib.WithDescription("Launch a child OpenCode session. By default it shares the parent's working directory; set worktree=true to run it in a fresh git worktree instead."),
		mcplib.WithString("session_id",
			mcplib.Required(),
			mcplib.Description("Parent session ID."),
		),
		mcplib.WithString("intent",
			mcplib.Required(),
			mcplib.Description("Sub-task for the child session."),
		),
		mcplib.WithString("model",
			mcplib.Description(`Optional "provider/model" reference for the child's first message. Defaults to the platform default.`),
		),
		mcplib.WithString("agent",
			mcplib.Description(`Optional composer agent/role for the child's first message ("build", "plan", or a subagent name). Defaults to the platform default.`),
		),
		mcplib.WithString("reasoning",
			mcplib.Description(`Optional model reasoning/thinking-budget for the first message ("high", "max", "low"). Only meaningful when the model exposes variants.`),
		),
		mcplib.WithArray("permission",
			mcplib.Description("Optional permission ruleset applied to the child session at creation. Each entry is {permission, pattern, action} where action is allow|deny|ask. Replaces the child's ruleset outright."),
			mcplib.Items(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"permission": map[string]interface{}{"type": "string"},
					"pattern":    map[string]interface{}{"type": "string"},
					"action":     map[string]interface{}{"type": "string"},
				},
			}),
		),
		mcplib.WithBoolean("worktree",
			mcplib.Description("Run the child in a fresh git worktree instead of the parent's working directory. Defaults to false. Requires branch."),
		),
		mcplib.WithString("branch",
			mcplib.Description("Branch name for the worktree. Required when worktree=true."),
		),
		mcplib.WithString("base_ref",
			mcplib.Description("Base ref for the worktree. Defaults to the repo default branch. Only used when worktree=true."),
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

// addSplitTools registers the new_session tool on the MCP server.
func addSplitTools(s *server.MCPServer, t *splitTools) {
	s.AddTool(newSessionTool(), t.handleNewSession)
}

// handleNewSession handles the new_session tool call. It dispatches to the
// worktree path when worktree=true, otherwise creates the child in the
// parent's working directory.
func (t *splitTools) handleNewSession(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	sessionID, err := req.RequireString("session_id")
	if err != nil {
		return mcplib.NewToolResultError("session_id is required"), nil
	}
	intent, err := req.RequireString("intent")
	if err != nil {
		return mcplib.NewToolResultError("intent is required"), nil
	}
	settings := parseSessionSettings(req)

	if req.GetBool("worktree", false) {
		return t.launchWorktree(ctx, req, sessionID, intent, settings)
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

	// Inherit the parent's always-allow permissions (issue #101).
	inherited, inheritedCount, inheritErr := t.inheritedRules(sessionID)

	// Launch the child session.
	childID, err := t.launcher.Launch(ctx, LaunchRequest{
		ParentSessionID: sessionID,
		Platform:        t.platform,
		Directory:       session.Directory,
		Intent:          intent,
		ComposedPrompt:  prompt,
		Model:           settings.Model,
		Agent:           settings.Agent,
		Reasoning:       settings.Reasoning,
		PermissionRules: mergeInheritedRules(settings.PermissionRules, inherited),
	})
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("launching child session: %v", err)), nil
	}

	result := map[string]interface{}{
		"child_session_id": childID,
		"status":           "starting",
		"intent":           intent,
	}
	addInheritanceResult(result, inheritedCount, inheritErr)
	return toolResultJSON(result), nil
}

// addInheritanceResult adds the issue-#101 result fields to a tool
// result map: permissionsInherited (bool), permissionsInheritedCount
// (int), and permissionsInheritError (only when non-empty).
func addInheritanceResult(result map[string]interface{}, count int, errNote string) {
	result["permissionsInherited"] = count > 0
	result["permissionsInheritedCount"] = count
	if errNote != "" {
		result["permissionsInheritError"] = errNote
	}
}

// launchWorktree handles the worktree=true branch of new_session.
func (t *splitTools) launchWorktree(ctx context.Context, req mcplib.CallToolRequest, sessionID, intent string, settings sessionSettings) (*mcplib.CallToolResult, error) {
	branch, err := req.RequireString("branch")
	if err != nil {
		return mcplib.NewToolResultError("branch is required when worktree=true"), nil
	}
	baseRef := req.GetString("base_ref", "")

	opts := parseContextOptions(req)

	// Look up the parent session's directory to find the repo root.
	session, err := t.composer.db.GetSession(sessionID)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("session not found: %v", err)), nil
	}

	repoRoot, err := git.ResolveRepoRoot(ctx, session.Directory)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("resolving repo root: %v", err)), nil
	}

	// If no base_ref provided, resolve the default.
	if baseRef == "" {
		baseRef = git.ResolveBaseRef(ctx, repoRoot)
	}

	// Point the prompt at the worktree dir that's about to be created, not
	// the parent checkout.
	opts.DirOverride = git.WorktreePathFor(repoRoot, branch)

	// Compose the enriched prompt.
	prompt, err := t.composer.Compose(ctx, sessionID, intent, opts)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("composing prompt: %v", err)), nil
	}

	// Inherit the parent's always-allow permissions (issue #101).
	inherited, inheritedCount, inheritErr := t.inheritedRules(sessionID)

	childID, wtResult, err := t.launcher.LaunchWithWorktree(
		ctx,
		LaunchRequest{
			ParentSessionID: sessionID,
			Platform:        t.platform,
			Intent:          intent,
			ComposedPrompt:  prompt,
			Model:           settings.Model,
			Agent:           settings.Agent,
			Reasoning:       settings.Reasoning,
			PermissionRules: mergeInheritedRules(settings.PermissionRules, inherited),
		},
		git.CreateWorktreeRequest{
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
	addInheritanceResult(result, inheritedCount, inheritErr)
	return toolResultJSON(result), nil
}

// sessionSettings holds the create-time knobs shared by both new_session
// branches: model/agent/reasoning for the first message and an optional
// permission ruleset applied to the child at creation.
type sessionSettings struct {
	Model           string
	Agent           string
	Reasoning       string
	PermissionRules []platforms.PermissionRule
}

// parseSessionSettings extracts model/agent/reasoning/permission from the
// tool request. Missing fields default to empty (platform defaults).
func parseSessionSettings(req mcplib.CallToolRequest) sessionSettings {
	return sessionSettings{
		Model:           req.GetString("model", ""),
		Agent:           req.GetString("agent", ""),
		Reasoning:       req.GetString("reasoning", ""),
		PermissionRules: parsePermissionRules(req),
	}
}

// parsePermissionRules decodes the "permission" array param into typed
// rules. Missing or malformed input yields nil (no rules applied).
func parsePermissionRules(req mcplib.CallToolRequest) []platforms.PermissionRule {
	raw, ok := req.GetArguments()["permission"]
	if !ok || raw == nil {
		return nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var rules []platforms.PermissionRule
	if err := json.Unmarshal(b, &rules); err != nil {
		return nil
	}
	return rules
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
