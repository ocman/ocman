package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/NoUseFreak/ocman/internal/loops"
)

// decodeInto JSON-roundtrips an arbitrary tool argument (typically a
// nested object) into a typed struct. Silently ignores nil/!ok inputs so
// an omitted optional object leaves the target at its zero value.
func decodeInto(v interface{}, target interface{}) {
	if v == nil {
		return
	}
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	_ = json.Unmarshal(b, target)
}

// loopService is the subset of loops.Service the MCP tools need. Defined
// as an interface so tests can substitute a fake without a full service.
type loopService interface {
	Create(ctx context.Context, spec loops.LoopSpec) (loops.LoopView, error)
	List(ctx context.Context, f loops.LoopFilter) ([]loops.LoopView, error)
	Get(ctx context.Context, id string) (loops.LoopDetail, error)
	Delete(ctx context.Context, id string) error
	Pause(ctx context.Context, id string) error
	Resume(ctx context.Context, id string) error
	Step(ctx context.Context, id string) error
}

// loopTools holds the dependencies for the loop tool handlers.
type loopTools struct {
	svc      loopService
	platform string
}

// addLoopTools registers the agent-loops MCP tools. No-op when svc is nil
// (loops unavailable, e.g. no state DB).
func addLoopTools(s *server.MCPServer, t *loopTools) {
	if t == nil || t.svc == nil {
		return
	}
	for _, st := range loopServerTools(t) {
		s.AddTool(st.Tool, st.Handler)
	}
}

// loopServerTools returns the loop tool entries. Shared by addLoopTools
// and ServerTools so the HTTP and mcptest paths register identical tools.
func loopServerTools(t *loopTools) []server.ServerTool {
	if t == nil || t.svc == nil {
		return nil
	}
	return []server.ServerTool{
		{Tool: createLoopTool(), Handler: t.handleCreateLoop},
		{Tool: listLoopsTool(), Handler: t.handleListLoops},
		{Tool: getLoopStatusTool(), Handler: t.handleGetLoopStatus},
		{Tool: loopControlTool("delete_loop", "Delete a loop (stops it permanently and removes it from the list)."), Handler: t.control("delete")},
		{Tool: loopControlTool("pause_loop", "Pause a loop (resume later)."), Handler: t.control("pause")},
		{Tool: loopControlTool("resume_loop", "Resume a paused loop."), Handler: t.control("resume")},
		{Tool: loopControlTool("step_loop", "Run one loop cycle then pause."), Handler: t.control("step")},
	}
}

func createLoopTool() mcplib.Tool {
	return mcplib.NewTool("create_loop",
		mcplib.WithDescription("Create a self-driving loop that prompts agents on a trigger until a stop condition is met. Requires a budget (max_cost_usd or max_tokens)."),
		mcplib.WithString("root_session_id", mcplib.Required(), mcplib.Description("Session the loop is anchored to.")),
		mcplib.WithString("parent_loop_id", mcplib.Description("Make this a sub-loop of an existing loop (its cost/tokens roll up into the parent's budget and it nests under the parent in the UI).")),
		mcplib.WithString("title", mcplib.Description("Short human-readable title.")),
		mcplib.WithString("directory", mcplib.Description("Working directory / project path.")),
		mcplib.WithString("pattern", mcplib.Description("pr_address | orchestrator | heartbeat | linear")),
		mcplib.WithString("trigger_type", mcplib.Required(), mcplib.Description("child_complete | schedule | cron | pr_event | turn_complete")),
		mcplib.WithObject("trigger_config", mcplib.Description("Trigger settings (interval_seconds, cron_expr, pr_number, target_session_id).")),
		mcplib.WithString("action_type", mcplib.Required(), mcplib.Description("prompt_root | prompt_child | spawn_child | spawn_worktree")),
		mcplib.WithString("action_template", mcplib.Description("Prompt template. Placeholders: {{iteration}} {{project}} {{last_summary}} {{trigger}} {{pr_number}}.")),
		mcplib.WithString("model", mcplib.Description("Optional model reference to use for loop prompts, e.g. provider/model.")),
		mcplib.WithString("session_mode", mcplib.Description("prompt_root session strategy: 'fresh' (new dedicated session each iteration, default) or 'reuse' (re-prompt the loop's session).")),
		mcplib.WithObject("stop_conditions", mcplib.Required(), mcplib.Description("max_iterations (required), max_cost_usd or max_tokens (one required), max_duration, error_streak, goal_predicate.")),
	)
}

func listLoopsTool() mcplib.Tool {
	return mcplib.NewTool("list_loops",
		mcplib.WithDescription("List loops, optionally filtered by session."),
		mcplib.WithString("root_session_id", mcplib.Description("Filter by anchoring session.")),
	)
}

func getLoopStatusTool() mcplib.Tool {
	return mcplib.NewTool("get_loop_status",
		mcplib.WithDescription("Get a loop's state, iteration, budget consumed, and last summary."),
		mcplib.WithString("loop_id", mcplib.Required(), mcplib.Description("Loop ID.")),
	)
}

func loopControlTool(name, desc string) mcplib.Tool {
	return mcplib.NewTool(name,
		mcplib.WithDescription(desc),
		mcplib.WithString("loop_id", mcplib.Required(), mcplib.Description("Loop ID.")),
	)
}

func (t *loopTools) handleCreateLoop(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	rootSession, err := req.RequireString("root_session_id")
	if err != nil {
		return mcplib.NewToolResultError("root_session_id is required"), nil
	}
	spec := loops.LoopSpec{
		Platform:       t.platform,
		RootSessionID:  rootSession,
		ParentLoopID:   req.GetString("parent_loop_id", ""),
		Title:          req.GetString("title", ""),
		Directory:      req.GetString("directory", ""),
		Pattern:        req.GetString("pattern", ""),
		TriggerType:    req.GetString("trigger_type", ""),
		ActionType:     req.GetString("action_type", ""),
		ActionTemplate: req.GetString("action_template", ""),
		Model:          req.GetString("model", ""),
		SessionMode:    req.GetString("session_mode", ""),
	}
	if m := req.GetArguments(); m != nil {
		decodeInto(m["trigger_config"], &spec.TriggerConfig)
		decodeInto(m["stop_conditions"], &spec.StopConditions)
	}

	view, err := t.svc.Create(ctx, spec)
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	return toolResultJSON(loopStatusSummary(view)), nil
}

func (t *loopTools) handleListLoops(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	views, err := t.svc.List(ctx, loops.LoopFilter{RootSessionID: req.GetString("root_session_id", "")})
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	out := make([]map[string]interface{}, 0, len(views))
	for _, v := range views {
		out = append(out, loopStatusSummary(v))
	}
	return toolResultJSON(out), nil
}

func (t *loopTools) handleGetLoopStatus(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	id, err := req.RequireString("loop_id")
	if err != nil {
		return mcplib.NewToolResultError("loop_id is required"), nil
	}
	detail, err := t.svc.Get(ctx, id)
	if err != nil {
		return mcplib.NewToolResultError(err.Error()), nil
	}
	summary := loopStatusSummary(detail.LoopView)
	summary["child_count"] = len(detail.Children)
	return toolResultJSON(summary), nil
}

// control returns a handler for the stop/pause/resume/step tools.
func (t *loopTools) control(action string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		id, err := req.RequireString("loop_id")
		if err != nil {
			return mcplib.NewToolResultError("loop_id is required"), nil
		}
		switch action {
		case "delete":
			err = t.svc.Delete(ctx, id)
		case "pause":
			err = t.svc.Pause(ctx, id)
		case "resume":
			err = t.svc.Resume(ctx, id)
		case "step":
			err = t.svc.Step(ctx, id)
		default:
			err = fmt.Errorf("unknown action %q", action)
		}
		if err != nil {
			return mcplib.NewToolResultError(err.Error()), nil
		}
		return toolResultJSON(map[string]interface{}{"success": true, "loop_id": id, "action": action}), nil
	}
}

// loopStatusSummary builds the compact status object returned by the tools.
func loopStatusSummary(v loops.LoopView) map[string]interface{} {
	out := map[string]interface{}{
		"loop_id":   v.ID,
		"title":     v.Title,
		"state":     v.State,
		"iteration": v.Iteration,
		"trigger":   v.TriggerType,
		"action":    v.ActionType,
	}
	if v.StopConditionsDecoded.MaxIterations > 0 {
		out["max_iterations"] = v.StopConditionsDecoded.MaxIterations
	}
	if v.StopConditionsDecoded.MaxCostUSD > 0 {
		out["cost_usd"] = v.CostUSD
		out["max_cost_usd"] = v.StopConditionsDecoded.MaxCostUSD
	}
	if v.LastSummary != "" {
		out["last_summary"] = v.LastSummary
	}
	return out
}
