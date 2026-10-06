package mcp

import (
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/NoUseFreak/ocman/internal/factory"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

func (t *factoryTools) handleGraphAction(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	epicID := req.GetString("epic_id", "")
	var result *mcplib.CallToolResult
	if req.GetString("action", "") == "submit_scope_plan" {
		service, ok := t.svc.(interface {
			SubmitScopePlan(context.Context, factory.SubmitProposalRequest) (factory.ProposalRevision, error)
		})
		if !ok {
			return mcplib.NewToolResultError("factory action is not permitted"), nil
		}
		manifest, invalid := factoryProposalManifest(req)
		if invalid != nil {
			return invalid, nil
		}
		proposal, err := service.SubmitScopePlan(ctx, factory.SubmitProposalRequest{EpicID: epicID, Manifest: manifest, RationaleMarkdown: req.GetString("rationale_markdown", ""), AttemptID: req.GetString("attempt_id", ""), AttemptToken: req.GetString("attempt_token", "")})
		if err != nil {
			return factoryToolError(err), nil
		}
		result = toolResultJSON(proposal)
	} else {
		mutator, ok := t.svc.(interface {
			MutateGraph(context.Context, factory.GraphMutation) error
		})
		if !ok {
			return mcplib.NewToolResultError("factory action is not permitted"), nil
		}
		raw, err := req.RequireString("mutation_json")
		var mutation factory.GraphMutation
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err != nil || decoder.Decode(&mutation) != nil || decoder.Decode(&struct{}{}) != io.EOF {
			return mcplib.NewToolResultError("mutation_json is invalid"), nil
		}
		mutation.Actor = "mcp"
		if err := mutator.MutateGraph(ctx, mutation); err != nil {
			return factoryToolError(err), nil
		}
		epicID = mutation.EpicID
		result = toolResultJSON(map[string]string{"status": "awaiting_approval"})
	}
	result.Content = append(result.Content, mcplib.NewTextContent("The revised graph awaits human approval: "+factoryCardMarker(epicID, "", "approve_plan")+". New work cannot run until the user approves it. "+factoryCardHandoff))
	return result, nil
}
