package pluginsmod

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"slices"
	"strconv"

	plugins "github.com/ocman/ocman/sdk/protocol"
)

// agentToolOutcome is either the plugin's result or a recorded request for
// the user's confirmation; the operation has not run in the second case.
type agentToolOutcome struct {
	result       plugins.ToolResult
	confirmation *toolConfirmation
}

func wireError(category plugins.ErrorCategory) error { return &plugins.WireError{Category: category} }

// agentToolAllowed is the authorization for one tool, checked against the
// current durable row at admission, dispatch and return.
func agentToolAllowed(name string) func(plugins.Description, []string) error {
	return func(d plugins.Description, grants []string) error {
		t, ok := d.Tool(name)
		if !ok {
			return wireError(plugins.ErrorNotFound)
		}
		if !d.ToolsAllowed(grants) || !t.Granted(grants) {
			return wireError(plugins.ErrorPermissionDenied)
		}
		return nil
	}
}

var agentToolStatusErrors = map[string]plugins.ErrorCategory{
	agentToolDisabled:   plugins.ErrorUnavailable,
	agentToolStopped:    plugins.ErrorUnavailable,
	agentToolNotGranted: plugins.ErrorPermissionDenied,
	agentToolConflict:   plugins.ErrorUnavailable,
}

// admitAgentTool returns the current declaration, sorted grants and process
// for an available tool, or why it is not callable.
func (m *Module) admitAgentTool(ctx context.Context, pluginID, name string) (plugins.Description, []string, *plugins.Process, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	states, err := m.agentToolStatesLocked(ctx)
	if err != nil {
		return plugins.Description{}, nil, nil, err
	}
	i := slices.IndexFunc(states, func(s agentToolState) bool { return s.PluginID == pluginID && s.Tool.Name == name })
	if i < 0 {
		return plugins.Description{}, nil, nil, wireError(plugins.ErrorNotFound)
	}
	if category, refused := agentToolStatusErrors[states[i].Status]; refused {
		return plugins.Description{}, nil, nil, wireError(category)
	}
	var description plugins.Description
	var grants []string
	err = m.store.WithPluginAuthorization(ctx, pluginID, func(d plugins.Description, approved []string) error {
		if err := agentToolAllowed(name)(d, approved); err != nil {
			return err
		}
		description, grants = d, slices.Sorted(slices.Values(approved))
		return nil
	})
	return description, grants, m.processes[pluginID], err
}

// callAgentTool validates, confirms and dispatches one agent call. args are
// the canonical arguments without host-reserved properties.
func (m *Module) callAgentTool(ctx context.Context, pluginID, name string, args []byte, confirmationID string) (agentToolOutcome, error) {
	if len(args) > plugins.MaxToolArgumentBytes {
		return agentToolOutcome{}, &plugins.ToolArgumentError{Path: "arguments", Reason: "must be at most " + strconv.Itoa(plugins.MaxToolArgumentBytes) + " bytes"}
	}
	description, grants, process, err := m.admitAgentTool(ctx, pluginID, name)
	if err != nil {
		return agentToolOutcome{}, err
	}
	tool, _ := description.Tool(name)
	if err := plugins.ValidateToolArguments(tool.InputSchema, args); err != nil {
		return agentToolOutcome{}, err
	}
	display := plugins.MaskToolArguments(tool.InputSchema, args)
	var confirmed *plugins.ToolConfirmed
	if confirmationID != "" {
		if confirmed, err = m.toolConfirmations.redeem(confirmationID, pluginID, name, args, description, process); err != nil {
			return agentToolOutcome{}, err
		}
	} else if tool.Confirmation != "" {
		c, err := m.toolConfirmations.request(pluginID, name, "", tool.Confirmation, display, args, description, process)
		return agentToolOutcome{confirmation: c}, err
	}
	inv := plugins.ToolInvocation{Tool: name, Arguments: args, Confirmation: confirmed,
		Context: plugins.ToolContext{Principal: plugins.ToolPrincipalAgent, Transport: plugins.ToolTransportMCP, Grants: grants}}
	result, err := m.dispatchAgentTool(ctx, pluginID, tool, inv)
	if err != nil {
		return agentToolOutcome{}, err
	}
	if c := result.Confirmation; c != nil {
		record, err := m.toolConfirmations.request(pluginID, name, c.Key, c.Text, plugins.RedactToolArguments(display, c.Redact), args, description, process)
		return agentToolOutcome{confirmation: record}, err
	}
	return agentToolOutcome{result: result}, nil
}

// dispatchAgentTool sends one invoke under the lifecycle lock, atomically with
// revocation, and discards the result if authority was lost meanwhile.
func (m *Module) dispatchAgentTool(ctx context.Context, pluginID string, tool plugins.ToolDescriptor, inv plugins.ToolInvocation) (plugins.ToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, tool.Timeout())
	defer cancel()
	params, err := json.Marshal(inv)
	if err != nil {
		return plugins.ToolResult{}, err
	}
	declared, _ := json.Marshal(tool)
	allowed := func(d plugins.Description, grants []string) error {
		if err := agentToolAllowed(tool.Name)(d, grants); err != nil {
			return err
		}
		// The arguments were validated against this exact declaration.
		current, _ := d.Tool(tool.Name)
		if raw, _ := json.Marshal(current); !bytes.Equal(raw, declared) {
			return wireError(plugins.ErrorConflict)
		}
		return nil
	}
	var replies <-chan plugins.Reply
	m.mu.Lock()
	if m.store == nil {
		m.mu.Unlock()
		return plugins.ToolResult{}, plugins.ErrUnavailable
	}
	err = m.store.WithPluginAuthorization(ctx, pluginID, func(d plugins.Description, grants []string) error {
		if err := allowed(d, grants); err != nil {
			return err
		}
		p := m.processes[pluginID]
		if p == nil {
			return plugins.ErrUnavailable
		}
		deadline, _ := ctx.Deadline()
		var err error
		replies, err = p.Call(ctx, plugins.Call{OperationID: "tool:" + rand.Text(), Capability: plugins.ToolCapability.Name,
			Version: plugins.ToolCapability.Version, Method: plugins.ToolInvokeMethod, DeadlineUnixMS: deadline.UnixMilli(), Params: params})
		return err
	})
	m.mu.Unlock()
	if errors.Is(err, plugins.ErrBusy) {
		return plugins.ToolResult{}, wireError(plugins.ErrorUnavailable)
	}
	if err != nil {
		return plugins.ToolResult{}, err
	}
	var reply plugins.Reply
	select {
	case <-ctx.Done():
		return plugins.ToolResult{}, ctx.Err()
	case reply = <-replies:
	}
	if reply.Err != nil {
		return plugins.ToolResult{}, reply.Err
	}
	// invoke is unary: a chunk is a protocol error, not partial output.
	result := reply.Message.Result
	if reply.Message.Type != plugins.TypeResult || result == nil {
		return plugins.ToolResult{}, wireError(plugins.ErrorInternal)
	}
	if result.Error != nil {
		return plugins.ToolResult{}, result.Error
	}
	if err := m.store.WithPluginAuthorization(ctx, pluginID, allowed); err != nil {
		return plugins.ToolResult{}, wireError(plugins.ErrorPermissionDenied)
	}
	decoded, err := plugins.DecodeToolResult(result.Value)
	if err != nil {
		return plugins.ToolResult{}, wireError(plugins.ErrorInternal)
	}
	return decoded, nil
}
