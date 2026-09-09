package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"

	"github.com/NoUseFreak/ocman/internal/factory"
	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type factoryUnblockService interface {
	ConsumeFactoryUnblock(string, string) bool
	ReopenIssue(context.Context, string, string) error
	MutateFactoryUnblock(context.Context, factory.GraphMutation) error
}

func factoryUnblockServerTools(service any) []server.ServerTool {
	svc, ok := service.(factoryUnblockService)
	if !ok {
		return nil
	}
	tool := mcplib.NewTool("factory_unblock",
		mcplib.WithDescription("Executes a Factory unblock action after the user approves it in the current conversation."),
		mcplib.WithString("action", mcplib.Required()),
		mcplib.WithString("epic_id", mcplib.Required()),
		mcplib.WithString("unblock_token", mcplib.Required()),
		mcplib.WithString("issue_id"),
		mcplib.WithString("mutation_json"),
	)
	return []server.ServerTool{{Tool: tool, Handler: func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		action, err := req.RequireString("action")
		if err != nil {
			return mcplib.NewToolResultError("action is required"), nil
		}
		epicID, err := req.RequireString("epic_id")
		if err != nil {
			return mcplib.NewToolResultError("epic_id is required"), nil
		}
		token, err := req.RequireString("unblock_token")
		if err != nil {
			return mcplib.NewToolResultError("unblock action is not authorized"), nil
		}
		switch action {
		case "reopen":
			issueID, err := req.RequireString("issue_id")
			if err != nil {
				return mcplib.NewToolResultError("issue_id is required"), nil
			}
			if !svc.ConsumeFactoryUnblock(token, epicID) {
				return mcplib.NewToolResultError("unblock action is not authorized"), nil
			}
			if err := svc.ReopenIssue(ctx, epicID, issueID); err != nil {
				return factoryToolError(err), nil
			}
		case "mutate_graph":
			raw, err := req.RequireString("mutation_json")
			var mutation factory.GraphMutation
			decoder := json.NewDecoder(bytes.NewReader([]byte(raw)))
			decoder.DisallowUnknownFields()
			if err != nil || decoder.Decode(&mutation) != nil || decoder.Decode(&struct{}{}) != io.EOF {
				return mcplib.NewToolResultError("mutation_json is invalid"), nil
			}
			if !svc.ConsumeFactoryUnblock(token, epicID) {
				return mcplib.NewToolResultError("unblock action is not authorized"), nil
			}
			mutation.EpicID, mutation.Actor = epicID, "mcp_unblock"
			if err := svc.MutateFactoryUnblock(ctx, mutation); err != nil {
				return factoryToolError(err), nil
			}
		default:
			return mcplib.NewToolResultError("unsupported unblock action"), nil
		}
		return toolResultJSON(map[string]string{"status": "ok"}), nil
	}}}
}
