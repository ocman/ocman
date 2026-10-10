package mcp

import (
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/NoUseFreak/ocman/internal/factory"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

var factoryGraphAction = factoryAction{
	name:        "mutate_graph",
	description: "Creates, edits, reparents, links, unlinks, or soft-deletes local Factory Issues unless they are in progress or closed. Propose the complete change in one call using action batch, epicId, mutations, and rationaleMarkdown. The Markdown rationale must summarize what changed and why; it is rendered beside graph approval. Batch edits run in order, inherit the outer epicId and actor, and commit atomically as one proposal revision. Use issues to inspect IDs first; create assigns the next child ID under parentId. Nested batches and cross-Epic edits are rejected. Single edits remain supported. New work cannot run until the user approves the exact revision. Dependency types are blocks, on_failure, and merge_gated; merge_gated must target another project's Delivery.",
	example:     `{"action":"mutate_graph","mutation_json":"{\"action\":\"batch\",\"epicId\":\"epic-1\",\"rationaleMarkdown\":\"## Changes\\nAdd a regression task.\\n\\n## Why\\nVerification found an uncovered case.\",\"mutations\":[{\"action\":\"create\",\"parentId\":\"epic-1.1\",\"kind\":\"task\",\"title\":\"Cover the missing case\"}]}"}`,
	required:    []string{"mutation_json"},
	output:      map[string]string{"status": "awaiting_approval"},
	errors:      []string{"mutation_json is required", "mutation_json is invalid", "factory request failed"},
}

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
