package pluginsmod

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/state"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

var _ module.DynamicToolProvider = (*Module)(nil)

// Agent tool statuses reported in Settings. Only available tools are listed
// to agents and routable.
const (
	agentToolAvailable  = "available"
	agentToolDisabled   = "disabled"
	agentToolStopped    = "stopped"
	agentToolNotGranted = "not-granted"
	agentToolConflict   = "conflict"
)

// agentToolState is one declared tool of a local registration.
type agentToolState struct {
	PluginID   string                 `json:"pluginId"`
	PluginName string                 `json:"pluginName"`
	Tool       plugins.ToolDescriptor `json:"tool"`
	Status     string                 `json:"status"`

	description plugins.Description
}

// SetBuiltinTools records the MCP server's own tool names, which always win.
func (m *Module) SetBuiltinTools(names []string) {
	m.toolNamesMu.Lock()
	defer m.toolNamesMu.Unlock()
	m.builtinTools = slices.Clone(names)
}

func (m *Module) builtinToolNames() []string {
	m.toolNamesMu.Lock()
	defer m.toolNamesMu.Unlock()
	return m.builtinTools
}

// agentToolStates derives every declared tool's status from the durable
// registrations; running reports whether a registration's process serves.
func (m *Module) agentToolStates(ctx context.Context, running func(state.PluginRegistration) bool) ([]agentToolState, error) {
	if m.store == nil {
		return nil, plugins.ErrUnavailable
	}
	registrations, err := m.store.ListPlugins(ctx)
	if err != nil {
		return nil, err
	}
	builtin := m.builtinToolNames()
	claims := map[string]int{}
	for _, p := range registrations {
		if p.Enabled && !p.Removed {
			for _, t := range p.Description.Tools {
				claims[t.Name]++
			}
		}
	}
	var states []agentToolState
	for _, p := range registrations {
		d := p.Description
		for _, t := range d.Tools {
			s := agentToolState{PluginID: d.ID, PluginName: d.Name, Tool: t, Status: agentToolAvailable, description: d}
			switch {
			case !p.Enabled || p.Removed || p.Health.Status == "conflict":
				s.Status = agentToolDisabled
			case slices.Contains(builtin, t.Name) || plugins.ReservedToolName(t.Name) || claims[t.Name] > 1:
				s.Status = agentToolConflict
			case !d.ToolsAllowed(p.Grants) || !t.Granted(p.Grants):
				s.Status = agentToolNotGranted
			case !running(p):
				s.Status = agentToolStopped
			}
			states = append(states, s)
		}
	}
	return states, nil
}

// agentToolStatesLocked uses the live process table; m.mu must be held.
func (m *Module) agentToolStatesLocked(ctx context.Context) ([]agentToolState, error) {
	return m.agentToolStates(ctx, func(p state.PluginRegistration) bool { return m.processes[p.Description.ID] != nil })
}

// DynamicTools lists this machine's available plugin tools. It reads durable
// health instead of taking the lifecycle lock, so listing never waits for a
// plugin restart, and never calls a plugin. Each handler authorizes again.
func (m *Module) DynamicTools(ctx context.Context) []mcpserver.ServerTool {
	states, err := m.agentToolStates(ctx, func(p state.PluginRegistration) bool { return p.Health.Status == "ready" })
	if err != nil {
		return nil
	}
	var tools []mcpserver.ServerTool
	for _, s := range states {
		if s.Status != agentToolAvailable {
			continue
		}
		tool, ok := mcpPluginTool(s)
		if ok {
			tools = append(tools, mcpserver.ServerTool{Tool: tool, Handler: m.agentToolHandler(s.PluginID, s.Tool.Name)})
		}
	}
	return tools
}

func mcpPluginTool(s agentToolState) (mcplib.Tool, bool) {
	var schema map[string]json.RawMessage
	if json.Unmarshal(s.Tool.InputSchema, &schema) != nil {
		return mcplib.Tool{}, false
	}
	var properties map[string]json.RawMessage
	_ = json.Unmarshal(schema["properties"], &properties)
	if properties == nil {
		properties = map[string]json.RawMessage{}
	}
	properties[plugins.ToolConfirmationArgument] = json.RawMessage(`{"type":"string","description":"Only when resuming a call the user approved in ocman: the confirmationId from the earlier result. Repeat the exact same arguments."}`)
	schema["properties"], _ = json.Marshal(properties)
	raw, err := json.Marshal(schema)
	if err != nil {
		return mcplib.Tool{}, false
	}
	tool := mcplib.NewToolWithRawSchema(s.Tool.Name, s.Tool.Description+" (Plugin: "+s.PluginName+".)", raw)
	tool.Annotations.Title = s.Tool.Title
	if a := s.Tool.Annotations; a != nil {
		tool.Annotations.ReadOnlyHint, tool.Annotations.DestructiveHint = a.ReadOnly, a.Destructive
		tool.Annotations.IdempotentHint, tool.Annotations.OpenWorldHint = a.Idempotent, a.OpenWorld
	}
	return tool, true
}

// agentToolText is fixed host text per wire category; plugin diagnostics are
// never returned.
var agentToolText = map[plugins.ErrorCategory]string{
	plugins.ErrorInvalidArgument:  "the plugin rejected the request as invalid",
	plugins.ErrorPermissionDenied: "the plugin tool is not permitted; its grants may have been revoked in Settings → Plugins",
	plugins.ErrorNotFound:         "the plugin tool or the requested item was not found",
	plugins.ErrorConflict:         "the request conflicts with the plugin's current state; reload it and retry",
	plugins.ErrorUnavailable:      "the plugin tool is unavailable; the plugin may be disabled, stopped or busy",
	plugins.ErrorDeadlineExceeded: "the plugin tool timed out; it may still have made changes",
	plugins.ErrorCancelled:        "the plugin tool call was cancelled; it may still have made changes",
	plugins.ErrorInternal:         "the plugin tool failed",
}

func (m *Module) agentToolHandler(pluginID, name string) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		args := map[string]any{}
		for k, v := range req.GetArguments() {
			args[k] = v
		}
		confirmationID, _ := args[plugins.ToolConfirmationArgument].(string)
		delete(args, plugins.ToolIntentArgument)
		delete(args, plugins.ToolConfirmationArgument)
		raw, err := json.Marshal(args) // map keys sort: one canonical form per value
		if err != nil {
			return mcplib.NewToolResultError("arguments must be a JSON object"), nil
		}
		outcome, err := m.callAgentTool(ctx, pluginID, name, raw, confirmationID)
		return agentToolResult(pluginID, outcome, err), nil
	}
}

// neutralizeCards keeps plugin text from requesting host-rendered cards.
func neutralizeCards(text string) string { return strings.ReplaceAll(text, "[[ocman:", "[ [ocman:") }

func agentToolResult(pluginID string, outcome agentToolOutcome, err error) *mcplib.CallToolResult {
	var argument *plugins.ToolArgumentError
	var refused errConfirmation
	var wire *plugins.WireError
	switch {
	case errors.As(err, &argument):
		return mcplib.NewToolResultError("invalid arguments: " + argument.Error())
	case errors.As(err, &refused):
		return mcplib.NewToolResultError(string(refused))
	case err != nil:
		category := plugins.ErrorInternal
		if errors.As(err, &wire) {
			category = wire.Category
		} else if response := pluginResponse(nil, err); response.Error != nil {
			category = response.Error.Category
		}
		return mcplib.NewToolResultError(agentToolText[category])
	}
	if c := outcome.confirmation; c != nil {
		next := "Ask the user to approve this request in ocman (the card under this tool call, or Settings → Plugins → Agent tools), then call the same tool again with the same arguments and confirmation_id."
		text := "Confirmation required, nothing was changed. " + c.Text + "\n" + next +
			"\n[[ocman:card type=plugin-tool-confirmation plugin=" + pluginID + " confirmation=" + c.ID + "]]"
		return mcplib.NewToolResultStructured(map[string]any{"status": "confirmation_required", "confirmationId": c.ID, "prompt": c.Text, "next": next}, text)
	}
	r := outcome.result
	text := r.Text
	if text == "" {
		text = string(r.Data)
	}
	text = neutralizeCards(text)
	result := mcplib.NewToolResultText(text)
	if len(r.Data) > 0 {
		var data map[string]any
		_ = json.Unmarshal(r.Data, &data)
		result.StructuredContent = data
	}
	result.IsError = r.IsError
	return result
}
