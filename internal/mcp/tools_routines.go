package mcp

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/NoUseFreak/ocman/internal/routines"
	"github.com/NoUseFreak/ocman/internal/state"
	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type routineService interface {
	Create(context.Context, routines.Input) (state.Routine, error)
	Update(context.Context, string, routines.Input) (state.Routine, error)
	Get(context.Context, string) (state.Routine, error)
	List(context.Context, bool) ([]state.Routine, error)
	Delete(context.Context, string) error
	RunNow(context.Context, string) (state.RoutineRun, error)
	History(context.Context, string) ([]state.RoutineRun, error)
}

type routineTools struct{ svc routineService }

type routineAction struct {
	name, description, example string
	required, optional         []string
	output                     any
}

var routineInputRequired = []string{"name", "prompt", "directory"}
var routineInputOptional = []string{"remote_id", "agent", "model", "session_mode", "session_id", "schedule_kind", "timeout_ms", "at", "cron", "timezone", "enabled", "delete_after_success"}

var routineActions = []routineAction{
	{name: "help", description: "Describes every available routine action.", example: `{"action":"help"}`, output: "Routine action documentation"},
	{name: "list", description: "Lists active routines.", example: `{"action":"list"}`, output: "Routine[]"},
	{name: "get", description: "Gets one routine.", example: `{"action":"get","routine_id":"routine-1"}`, required: []string{"routine_id"}, output: "Routine"},
	{name: "create", description: "Creates a routine. Defaults to a disabled, unscheduled routine using a new session.", example: `{"action":"create","name":"Daily review","prompt":"Review open work","directory":"/repo"}`, required: routineInputRequired, optional: routineInputOptional, output: "Routine"},
	{name: "update", description: "Replaces one routine definition. Omitted optional fields use their defaults.", example: `{"action":"update","routine_id":"routine-1","name":"Daily review","prompt":"Review open work","directory":"/repo","enabled":true,"schedule_kind":"cron","cron":"0 9 * * 1-5","timezone":"Europe/Brussels"}`, required: append([]string{"routine_id"}, routineInputRequired...), optional: routineInputOptional, output: "Routine"},
	{name: "delete", description: "Soft-deletes one routine and disables future runs.", example: `{"action":"delete","routine_id":"routine-1"}`, required: []string{"routine_id"}, output: map[string]string{"status": "deleted"}},
	{name: "run", description: "Runs one routine now.", example: `{"action":"run","routine_id":"routine-1"}`, required: []string{"routine_id"}, output: "RoutineRun"},
	{name: "history", description: "Lists runs for one routine, newest first.", example: `{"action":"history","routine_id":"routine-1"}`, required: []string{"routine_id"}, output: "RoutineRun[]"},
}

func routineServerTools(tools *routineTools) []server.ServerTool {
	if tools == nil || tools.svc == nil {
		return nil
	}
	return []server.ServerTool{{Tool: mcplib.NewTool("routines",
		mcplib.WithDescription("Create, inspect, run, and manage routines. Use action help for schemas and examples."),
		mcplib.WithString("action", mcplib.Required()), mcplib.WithString("routine_id"), mcplib.WithString("name"), mcplib.WithString("prompt"),
		mcplib.WithString("directory"), mcplib.WithString("remote_id"), mcplib.WithString("agent"), mcplib.WithString("model"),
		mcplib.WithString("session_mode"), mcplib.WithString("session_id"), mcplib.WithString("schedule_kind"), mcplib.WithNumber("timeout_ms"),
		mcplib.WithNumber("at"), mcplib.WithString("cron"), mcplib.WithString("timezone"), mcplib.WithBoolean("enabled"), mcplib.WithBoolean("delete_after_success")), Handler: tools.handle}}
}

func addRoutineTools(s *server.MCPServer, tools *routineTools) {
	for _, tool := range routineServerTools(tools) {
		s.AddTool(tool.Tool, tool.Handler)
	}
}

func (t *routineTools) handle(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	action, err := req.RequireString("action")
	if err != nil {
		return mcplib.NewToolResultError("action is required"), nil
	}
	registered, ok := routineActionFor(action)
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
		return toolResultJSON(routineHelp()), nil
	case "list":
		items, err := t.svc.List(ctx, false)
		if err != nil {
			return routineToolError(err), nil
		}
		if items == nil {
			items = []state.Routine{}
		}
		return toolResultJSON(items), nil
	case "get":
		item, err := t.svc.Get(ctx, req.GetString("routine_id", ""))
		if err != nil {
			return routineToolError(err), nil
		}
		return toolResultJSON(item), nil
	case "create", "update":
		input, err := routineInput(req, arguments)
		if err != nil {
			return routineToolError(err), nil
		}
		var item state.Routine
		if action == "create" {
			item, err = t.svc.Create(ctx, input)
		} else {
			item, err = t.svc.Update(ctx, req.GetString("routine_id", ""), input)
		}
		if err != nil {
			return routineToolError(err), nil
		}
		return toolResultJSON(item), nil
	case "delete":
		if err := t.svc.Delete(ctx, req.GetString("routine_id", "")); err != nil {
			return routineToolError(err), nil
		}
		return toolResultJSON(map[string]string{"status": "deleted"}), nil
	case "run":
		run, err := t.svc.RunNow(ctx, req.GetString("routine_id", ""))
		if err != nil {
			return routineToolError(err), nil
		}
		return toolResultJSON(run), nil
	case "history":
		runs, err := t.svc.History(ctx, req.GetString("routine_id", ""))
		if err != nil {
			return routineToolError(err), nil
		}
		if runs == nil {
			runs = []state.RoutineRun{}
		}
		return toolResultJSON(runs), nil
	}
	return mcplib.NewToolResultError("unknown action"), nil
}

func routineActionFor(name string) (routineAction, bool) {
	for _, action := range routineActions {
		if action.name == name {
			return action, true
		}
	}
	return routineAction{}, false
}

func routineHelp() map[string]any {
	help := make(map[string]any, len(routineActions)+3)
	actions := make([]string, 0, len(routineActions))
	for _, action := range routineActions {
		actions = append(actions, action.name)
		help[action.name] = map[string]any{"required": append([]string{}, action.required...), "optional": append([]string{}, action.optional...), "action": action.description, "example": action.example, "output_schema": action.output}
	}
	help["actions"] = actions
	help["errors"] = []string{"action is required", "unknown action", "invalid routine", "routine not found", "routine name already exists", "routine already has an active shared-session run", "routine request failed"}
	help["rules"] = []string{"directory must be absolute", "session_mode is new, reuse, or existing; existing requires session_id", "schedule_kind is none, timeout, once, or cron", "timeout_ms is a positive millisecond delay", "at is a future Unix timestamp in milliseconds", "cron is a five-field expression and timezone is an IANA timezone"}
	return help
}

func routineInput(req mcplib.CallToolRequest, arguments map[string]any) (routines.Input, error) {
	timeoutMS := int64(req.GetInt("timeout_ms", 0))
	if timeoutMS > math.MaxInt64/int64(time.Millisecond) || timeoutMS < math.MinInt64/int64(time.Millisecond) {
		return routines.Input{}, routines.ErrValidation
	}
	return routines.Input{
		Name: req.GetString("name", ""), Prompt: req.GetString("prompt", ""), Directory: req.GetString("directory", ""),
		RemoteID: req.GetString("remote_id", ""), Agent: req.GetString("agent", ""), Model: req.GetString("model", ""),
		SessionMode: req.GetString("session_mode", routines.SessionNew), SessionID: req.GetString("session_id", ""),
		Schedule: routines.Schedule{Kind: req.GetString("schedule_kind", routines.ScheduleNone), Timeout: time.Duration(timeoutMS) * time.Millisecond, At: time.UnixMilli(int64(req.GetInt("at", 0))), Cron: req.GetString("cron", ""), Timezone: req.GetString("timezone", "")},
		Enabled:  boolArgument(arguments, "enabled"), DeleteAfterSuccess: boolArgument(arguments, "delete_after_success"),
	}, nil
}

func boolArgument(arguments map[string]any, name string) bool {
	value, _ := arguments[name].(bool)
	return value
}

func routineToolError(err error) *mcplib.CallToolResult {
	switch {
	case errors.Is(err, routines.ErrValidation):
		return mcplib.NewToolResultError(err.Error())
	case errors.Is(err, state.ErrRoutineNotFound):
		return mcplib.NewToolResultError("routine not found")
	case errors.Is(err, routines.ErrNameConflict):
		return mcplib.NewToolResultError("routine name already exists")
	case errors.Is(err, state.ErrRoutineRunActive):
		return mcplib.NewToolResultError("routine already has an active shared-session run")
	default:
		return mcplib.NewToolResultError("routine request failed")
	}
}
