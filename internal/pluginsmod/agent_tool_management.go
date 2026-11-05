package pluginsmod

import (
	"context"
	"encoding/json"

	"github.com/ocman/ocman/internal/state"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

type listedAgentTool struct {
	plugins.ToolDescriptor
	Status string `json:"status"`
}

type agentToolsReport struct {
	Tools         []listedAgentTool  `json:"tools"`
	Confirmations []toolConfirmation `json:"confirmations"`
}

// manageAgentTools serves the Settings report and the user's decision on a
// pending confirmation. m.mu must be held; decisions arrive only through the
// localhost-guarded management POST.
func (m *Module) manageAgentTools(ctx context.Context, p state.PluginRegistration, action string, input pluginManagementInput) (any, error) {
	id := p.Description.ID
	if action == "tools/confirm" {
		var confirmationID, decision string
		if json.Unmarshal(input.Values["confirmationId"], &confirmationID) != nil || json.Unmarshal(input.Values["decision"], &decision) != nil ||
			(decision != "approve" && decision != "deny") || len(input.Values) != 2 {
			return nil, state.ErrPluginInvalid
		}
		err := m.toolConfirmations.decide(id, confirmationID, decision == "approve", func(r *toolConfirmation) error {
			return m.store.WithPluginAuthorization(ctx, id, func(d plugins.Description, grants []string) error {
				if bindingOf(m.processes[id], d) != r.binding {
					return errPluginConflict
				}
				return agentToolAllowed(r.Tool)(d, grants)
			})
		})
		if err != nil {
			return nil, err
		}
	} else if action != "tools" {
		return nil, state.ErrPluginInvalid
	}
	states, err := m.agentToolStatesLocked(ctx)
	if err != nil {
		return nil, err
	}
	report := agentToolsReport{Tools: []listedAgentTool{}, Confirmations: m.toolConfirmations.list(id)}
	for _, s := range states {
		if s.PluginID == id {
			report.Tools = append(report.Tools, listedAgentTool{ToolDescriptor: s.Tool, Status: s.Status})
		}
	}
	return report, nil
}
