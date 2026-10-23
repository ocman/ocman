package factorymod

import (
	"context"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// factoryControlService backs factory_control. A call only records a
// proposal; Ocman renders it as a card and runs it when the user confirms.
type factoryControlService interface {
	ProposeFactoryControl(ctx context.Context, epicID, action string, args map[string]string, summary string) (string, error)
}

var factoryControlFields = []string{"issue_id", "mutation_json", "recovery_gate_id", "authority_gate_id", "project_gate_id", "decision", "response", "expected_revision", "expected_hash", "implementation_model"}

func factoryControlServerTools(svc factoryControlService) []server.ServerTool {
	options := []mcplib.ToolOption{
		mcplib.WithDescription("Proposes a change to a Factory Epic for user confirmation: claim_plan, materialize, or reopen (issue_id), mutate_graph (mutation_json), resolve_recovery (recovery_gate_id, decision resume|retry|cancel, response), resolve_authority (authority_gate_id, decision approve|reject), resolve_project (project_gate_id, decision approve|reject, response; the user must explicitly allow local execution on the confirmation card), decide_plan (decision approve|revise|reject, expected_revision, expected_hash, response, implementation_model), deliver_as_is, pause, resume, or stop. Stop ends all Epic work without delivery, preserving partial files and recording unfinished work as cancelled. Nothing runs until the user clicks Confirm."),
		mcplib.WithString("action", mcplib.Required()),
		mcplib.WithString("epic_id", mcplib.Required()),
		mcplib.WithString("summary", mcplib.Required(), mcplib.Description("One line telling the user what confirming does.")),
	}
	for _, field := range factoryControlFields {
		options = append(options, mcplib.WithString(field))
	}
	tool := mcplib.NewTool("factory_control", options...)
	return []server.ServerTool{{Tool: tool, Handler: func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		epicID := req.GetString("epic_id", "")
		args := map[string]string{}
		for _, field := range factoryControlFields {
			if value := req.GetString(field, ""); value != "" {
				args[field] = value
			}
		}
		id, err := svc.ProposeFactoryControl(ctx, epicID, req.GetString("action", ""), args, req.GetString("summary", ""))
		if err != nil {
			return factoryToolError(err), nil
		}
		result := toolResultJSON(map[string]string{"status": "awaiting_confirmation", "proposalId": id})
		result.Content = append(result.Content, mcplib.NewTextContent(factoryCardMarker(epicID, id, "control")+" "+factoryCardHandoff+" Do not repeat this call; Factory tells you the outcome."))
		return result, nil
	}}}
}
