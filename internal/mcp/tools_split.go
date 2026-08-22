package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/permissions"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

const childResultProgressInterval = 10 * time.Second

// permissionInheriter is the slice of *state.DB the split tools need to
// inherit the parent's always-allow permissions into a child (issue
// #101). Nil disables inheritance (no state DB).
type permissionInheriter interface {
	GetWorktreeInheritPermissions(context.Context) (bool, error)
	ListApprovedPermissions(context.Context, string, string) ([]state.ApprovedPermission, error)
}

// splitTools holds the dependencies for the new_session tool handler.
type splitTools struct {
	composer *PromptComposer
	launcher *SessionLauncher
	platform string // platform ID, e.g. "opencode"
	// inherit provides the parent's approved permissions and the
	// inherit-on/off setting. Nil = inheritance disabled.
	inherit      permissionInheriter
	results      *ChildResultBroker
	store        childResultStore
	disconnected func(context.Context, string)
}

type childResultStore interface {
	GetChildSession(context.Context, string) (*state.ChildSession, error)
	ListDisconnectedChildSessions(context.Context, string) ([]state.ChildSession, error)
	CompareAndSetChildResultDelivery(context.Context, string, string, string) (bool, error)
}

type childResultDeliveryStore interface {
	CompareAndSetChildResultDelivery(context.Context, string, string, string) (bool, error)
}

// inheritedRules builds the parent's always-allow ruleset for a child
// launch when the setting is on. Soft-fail: returns (nil, 0, note) on
// any error so the caller can proceed with the launch and surface the
// note. When inheritance is off or yields nothing, returns
// (nil, 0, "").
func (t *splitTools) inheritedRules(ctx context.Context, parentSessionID string) (rules []platforms.PermissionRule, count int, errNote string) {
	if t.inherit == nil || parentSessionID == "" {
		return nil, 0, ""
	}
	on, err := t.inherit.GetWorktreeInheritPermissions(ctx)
	if err != nil {
		log.WithError(err).Warn("mcp: reading worktree inherit-permissions setting")
		return nil, 0, "reading setting: " + err.Error()
	}
	if !on {
		return nil, 0, ""
	}
	var reader permissions.LiveRuleReader
	if t.launcher != nil && t.launcher.platform != nil {
		reader = liveRuleReaderFunc(t.launcher.platform.PermissionRules)
	}
	rules, count, err = permissions.BuildInheritedRulesWithLive(ctx, t.inherit, reader, t.platform, parentSessionID)
	if err != nil {
		log.WithError(err).Warn("mcp: building inherited permission rules")
		return nil, 0, "building rules: " + err.Error()
	}
	return rules, count, ""
}

// liveRuleReaderFunc adapts a Platform.PermissionRules method (ctx,
// sessionID) to the permissions.LiveRuleReader shape (platform,
// sessionID), discarding the unused platform arg and supplying a
// background context.
type liveRuleReaderFunc func(ctx context.Context, sessionID string) ([]platforms.PermissionRule, error)

func (f liveRuleReaderFunc) PermissionRules(_ string, sessionID string) ([]platforms.PermissionRule, error) {
	return f(context.Background(), sessionID)
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
		mcplib.WithDescription("Run a child OpenCode session. It waits for the terminal result by default; set wait=false to return immediately and deliver the final response to the parent asynchronously. Child sessions cannot create further children. By default it shares the parent's working directory; set worktree=true to run it in a fresh git worktree instead."),
		mcplib.WithString("session_id",
			mcplib.Required(),
			mcplib.Description("Parent session ID."),
		),
		mcplib.WithString("intent",
			mcplib.Required(),
			mcplib.Description("Sub-task for the child session."),
		),
		mcplib.WithString("model",
			mcplib.Description(`Optional "provider/model" reference for the child's first message. Defaults to the parent session's current model.`),
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
		mcplib.WithBoolean("wait",
			mcplib.Description("Wait for the child result. Defaults to true; false returns the child session ID immediately and delivers the completed turn to the parent asynchronously."),
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

func awaitSessionResultTool() mcplib.Tool {
	return mcplib.NewTool("await_session_result",
		mcplib.WithDescription("Wait for an asynchronous child or reconnect to a disconnected result wait without sending another prompt."),
		mcplib.WithString("session_id", mcplib.Required(), mcplib.Description("Parent session ID.")),
		mcplib.WithString("child_session_id", mcplib.Description("Child session ID. Omit when the parent has exactly one disconnected child.")),
	)
}

// addSplitTools registers the new_session tool on the MCP server.
func addSplitTools(s *server.MCPServer, t *splitTools) {
	s.AddTool(newSessionTool(), t.handleNewSession)
	s.AddTool(awaitSessionResultTool(), t.handleAwaitSessionResult)
}

// handleNewSession handles the new_session tool call. It dispatches to the
// worktree path when worktree=true, otherwise creates the child in the
// parent's working directory.
func (t *splitTools) handleNewSession(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	sessionID, err := req.RequireString("session_id")
	if err != nil {
		return mcplib.NewToolResultError("session_id is required"), nil
	}
	if t.store != nil {
		if _, err := t.store.GetChildSession(ctx, sessionID); err == nil {
			return mcplib.NewToolResultError("new_session is limited to one generation"), nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return mcplib.NewToolResultError(fmt.Sprintf("checking parent session: %v", err)), nil
		}
	}
	intent, err := req.RequireString("intent")
	if err != nil {
		return mcplib.NewToolResultError("intent is required"), nil
	}
	settings := parseSessionSettings(req)
	// Inherit the parent's latest/effective model when the caller omits
	// one, so a child split keeps running on the same model instead of
	// dropping to the platform default. An explicit model still wins.
	if settings.Model == "" {
		settings.Model = t.parentModel(ctx, sessionID)
	}

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
	session, err := t.composer.db.GetSession(ctx, sessionID)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("session not found: %v", err)), nil
	}

	// Inherit the parent's always-allow permissions (issue #101).
	inherited, inheritedCount, inheritErr := t.inheritedRules(ctx, sessionID)

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
		WaitForResult:   settings.WaitForResult,
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
	if settings.WaitForResult {
		if err := awaitChildResult(ctx, req, childID, result, t.results, t.store, t.disconnected); err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("waiting for child session: %v", err)), nil
		}
	}
	return toolResultJSON(result), nil
}

func awaitChildResult(ctx context.Context, req mcplib.CallToolRequest, childID string, result map[string]interface{}, results *ChildResultBroker, store childResultDeliveryStore, disconnected func(context.Context, string)) error {
	if results == nil {
		return nil
	}
	stopProgress := startChildResultProgress(ctx, req, childID)
	defer stopProgress()
	defer results.Unregister(childID)
	childResult, err := results.WaitOwned(ctx, childID)
	if err != nil {
		claimed := true
		if store != nil {
			claimed, _ = store.CompareAndSetChildResultDelivery(ctx, childID, "waiting", "disconnected")
		}
		if claimed && disconnected != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
			disconnected(ctx, childID)
		}
		return err
	}
	if store != nil {
		claimed, err := store.CompareAndSetChildResultDelivery(ctx, childID, "waiting", "delivered")
		if err != nil {
			return err
		}
		if !claimed {
			return fmt.Errorf("child session %s result waiter lost delivery ownership", childID)
		}
	}
	result["status"] = childResult.Status
	if childResult.Summary != "" {
		result["summary"] = childResult.Summary
	}
	return nil
}

func startChildResultProgress(ctx context.Context, req mcplib.CallToolRequest, childID string) func() {
	if req.Params.Meta == nil || req.Params.Meta.ProgressToken == nil {
		return func() {}
	}
	srv := server.ServerFromContext(ctx)
	if srv == nil {
		return func() {}
	}
	token := req.Params.Meta.ProgressToken
	done := make(chan struct{})
	go runChildResultProgress(ctx, done, childResultProgressInterval, func(step int) {
		if err := srv.SendNotificationToClient(ctx, "notifications/progress", map[string]interface{}{
			"progressToken": token,
			"progress":      step,
			"message":       fmt.Sprintf("Waiting for child session %s", childID),
		}); err != nil {
			log.WithError(err).WithField("childSessionID", childID).Debug("mcp: sending child result progress")
		}
	})
	return func() { close(done) }
}

func runChildResultProgress(ctx context.Context, done <-chan struct{}, interval time.Duration, notify func(step int)) {
	select {
	case <-ctx.Done():
		return
	case <-done:
		return
	default:
	}
	notify(1)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for step := 2; ; step++ {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			notify(step)
		}
	}
}

func (t *splitTools) handleAwaitSessionResult(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	parentID, err := req.RequireString("session_id")
	if err != nil {
		return mcplib.NewToolResultError("session_id is required"), nil
	}
	if t.store == nil || t.results == nil {
		return mcplib.NewToolResultError("child result recovery is unavailable"), nil
	}

	var child *state.ChildSession
	childID := req.GetString("child_session_id", "")
	if childID != "" {
		child, err = t.store.GetChildSession(ctx, childID)
		if err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("child session not found: %v", err)), nil
		}
		if child.ParentSessionID != parentID {
			return mcplib.NewToolResultError("child session does not belong to parent"), nil
		}
	} else {
		children, listErr := t.store.ListDisconnectedChildSessions(ctx, parentID)
		if listErr != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("finding disconnected child: %v", listErr)), nil
		}
		if len(children) == 0 {
			return mcplib.NewToolResultError("no disconnected child session found"), nil
		}
		if len(children) > 1 {
			return mcplib.NewToolResultError("multiple disconnected child sessions found; child_session_id is required"), nil
		}
		child = &children[0]
	}

	result := map[string]interface{}{
		"child_session_id": child.ID,
		"status":           child.Status,
		"intent":           child.Intent,
	}
	if child.ResultDelivery == "waiting" {
		return mcplib.NewToolResultError("child session result is still connected to its original call"), nil
	}
	if child.ResultDelivery == "detached" {
		return mcplib.NewToolResultError("child session predates asynchronous result delivery"), nil
	}
	if child.ResultDelivery == state.ChildResultAsyncQueueing || child.ResultDelivery == "delivered" {
		return mcplib.NewToolResultError("child session result belongs to asynchronous delivery"), nil
	}
	if isTerminalStatus(child.Status) {
		if child.ResultDelivery == state.ChildResultAsyncPending {
			claimed, claimErr := t.store.CompareAndSetChildResultDelivery(ctx, child.ID, state.ChildResultAsyncPending, "delivered")
			if claimErr != nil {
				return mcplib.NewToolResultError(fmt.Sprintf("claiming child result: %v", claimErr)), nil
			}
			if !claimed {
				return mcplib.NewToolResultError("child session result was claimed by asynchronous delivery"), nil
			}
		}
		if child.Summary != "" {
			result["summary"] = child.Summary
		}
		if child.ResultDelivery == "disconnected" {
			var claimed bool
			claimed, err = t.store.CompareAndSetChildResultDelivery(ctx, child.ID, "disconnected", "delivered")
			if err == nil && !claimed {
				return mcplib.NewToolResultError("child session result was claimed by another delivery"), nil
			}
		}
		if err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("marking child result delivered: %v", err)), nil
		}
		return toolResultJSON(result), nil
	}
	if child.ResultDelivery != "disconnected" && child.ResultDelivery != state.ChildResultAsyncPending {
		return mcplib.NewToolResultError(fmt.Sprintf("child session result is %s, not disconnected", child.ResultDelivery)), nil
	}

	if !t.results.Register(child.ID) {
		return mcplib.NewToolResultError("child session already has a result waiter"), nil
	}
	claimed, err := t.store.CompareAndSetChildResultDelivery(ctx, child.ID, child.ResultDelivery, "waiting")
	if err != nil {
		t.results.Unregister(child.ID)
		return mcplib.NewToolResultError(fmt.Sprintf("reconnecting child result: %v", err)), nil
	}
	if !claimed {
		t.results.Unregister(child.ID)
		return mcplib.NewToolResultError("child session result was claimed by another delivery"), nil
	}
	latest, err := t.store.GetChildSession(ctx, child.ID)
	if err != nil {
		t.results.Unregister(child.ID)
		return mcplib.NewToolResultError(fmt.Sprintf("refreshing child session: %v", err)), nil
	}
	if isTerminalStatus(latest.Status) {
		claimed, claimErr := t.store.CompareAndSetChildResultDelivery(ctx, child.ID, "waiting", "delivered")
		t.results.Unregister(child.ID)
		if claimErr != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("marking child result delivered: %v", claimErr)), nil
		}
		if !claimed {
			return mcplib.NewToolResultError("child session result was claimed by another delivery"), nil
		}
		result["status"] = latest.Status
		if latest.Summary != "" {
			result["summary"] = latest.Summary
		}
		return toolResultJSON(result), nil
	}
	if err := awaitChildResult(ctx, req, child.ID, result, t.results, t.store, t.disconnected); err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("waiting for child session: %v", err)), nil
	}
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

	// Look up the parent session's directory: it identifies the project
	// and, through the router, the host that owns it.
	session, err := t.composer.db.GetSession(ctx, sessionID)
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("session not found: %v", err)), nil
	}

	// Inherit the parent's always-allow permissions (issue #101). A pure
	// read of the parent, so it stays above the mutation: nothing here
	// needs the worktree, and anything read after the create would be
	// read while an orphan is already on disk.
	inherited, inheritedCount, inheritErr := t.inheritedRules(ctx, sessionID)

	// The owning host resolves the repo root, creates the worktree and
	// opens the session on the project's opencode instance. An empty
	// base_ref lets it pick its own default (AD-16: no local git here).
	wtResult, err := t.launcher.CreateWorktreeSession(ctx, WorktreeSessionRequest{
		ParentDir: session.Directory,
		Branch:    branch,
		NewBranch: true,
		BaseRef:   baseRef,
	})
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("launching worktree session: %v", err)), nil
	}

	// Point the prompt at the worktree the host actually created, not the
	// parent checkout.
	opts.DirOverride = wtResult.WorktreePath

	// Compose the enriched prompt. This can only run after the create —
	// only the owning host knows the worktree path the prompt points at —
	// so a failure here must not abort: the worktree and its session
	// already exist, and returning an error would strand both. Fall back
	// to the bare intent instead.
	prompt, err := t.composer.Compose(ctx, sessionID, intent, opts)
	if err != nil {
		log.WithFields(log.Fields{
			"parentSessionID": sessionID,
			"error":           err,
		}).Warn("mcp: composing worktree prompt failed; sending the bare intent")
		prompt = intent
	}

	childID := wtResult.SessionID
	if err := t.launcher.AttachChild(ctx, LaunchRequest{
		ParentSessionID: sessionID,
		Platform:        t.platform,
		Directory:       wtResult.WorktreePath,
		WorktreePath:    wtResult.WorktreePath,
		Branch:          wtResult.Branch,
		Intent:          intent,
		ComposedPrompt:  prompt,
		Model:           settings.Model,
		Agent:           settings.Agent,
		Reasoning:       settings.Reasoning,
		PermissionRules: mergeInheritedRules(settings.PermissionRules, inherited),
		WaitForResult:   settings.WaitForResult,
	}, childID); err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("launching worktree session: %v", err)), nil
	}

	result := map[string]interface{}{
		"child_session_id": childID,
		"worktree_path":    wtResult.WorktreePath,
		"branch":           wtResult.Branch,
		"status":           "starting",
		"intent":           intent,
	}
	addInheritanceResult(result, inheritedCount, inheritErr)
	if settings.WaitForResult {
		if err := awaitChildResult(ctx, req, childID, result, t.results, t.store, t.disconnected); err != nil {
			return mcplib.NewToolResultError(fmt.Sprintf("waiting for child session: %v", err)), nil
		}
	}
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
	WaitForResult   bool
}

// parseSessionSettings extracts model/agent/reasoning/permission from the
// tool request. Missing fields default to empty (platform defaults).
func parseSessionSettings(req mcplib.CallToolRequest) sessionSettings {
	return sessionSettings{
		Model:           req.GetString("model", ""),
		Agent:           req.GetString("agent", ""),
		Reasoning:       req.GetString("reasoning", ""),
		PermissionRules: parsePermissionRules(req),
		WaitForResult:   req.GetBool("wait", true),
	}
}

// parentModel returns the parent session's latest/effective model as a
// "provider/model" reference, scanning its messages newest-first for the
// first one that carries a model. Returns "" when the parent has no
// model-bearing message or the lookup fails (soft: never blocks a
// launch — the child just falls back to the platform default).
func (t *splitTools) parentModel(ctx context.Context, sessionID string) string {
	if t.composer == nil || t.composer.db == nil || sessionID == "" {
		return ""
	}
	msgs, err := t.composer.db.GetSessionMessages(ctx, sessionID)
	if err != nil {
		return ""
	}
	// GetSessionMessages returns ascending by time; walk backwards for the
	// latest model-bearing message.
	for i := len(msgs) - 1; i >= 0; i-- {
		var md struct {
			ProviderID string `json:"providerID"`
			ModelID    string `json:"modelID"`
			Model      *struct {
				ProviderID string `json:"providerID"`
				ModelID    string `json:"modelID"`
			} `json:"model"`
		}
		if err := json.Unmarshal(msgs[i].Data, &md); err != nil {
			continue
		}
		provider, model := md.ProviderID, md.ModelID
		if model == "" && md.Model != nil {
			provider, model = md.Model.ProviderID, md.Model.ModelID
		}
		if model == "" {
			continue
		}
		if provider == "" {
			return model
		}
		return provider + "/" + model
	}
	return ""
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
