package prworkflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	internalmcp "github.com/ocman/ocman/internal/mcp"
	"github.com/ocman/ocman/internal/module"
)

var _ module.ToolProvider = (*Module)(nil)

func (m *Module) Tools() []server.ServerTool {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/session/{id}/pull-requests", m.list)
	mux.HandleFunc("POST /api/session/{id}/pull-requests", m.change)
	mux.HandleFunc("POST /api/session/{id}/pull-request-feedback", m.feedback)
	if m.d.ProjectHandler != nil {
		mux.Handle("/api/project/", m.d.ProjectHandler)
	}
	return []server.ServerTool{{Tool: mcplib.NewTool("pull_requests",
		mcplib.WithDescription("Browse PRs and checks, launch PR sessions, link PRs to sessions, watch changes, and prepare feedback/check/conflict task drafts. Use action help for inputs and safety rules. Never merges or changes credentials."),
		mcplib.WithString("action", mcplib.Required()),
		mcplib.WithString("directory"), mcplib.WithString("remote_id"), mcplib.WithString("remote"),
		mcplib.WithString("session_id"), mcplib.WithString("platform"),
		mcplib.WithString("host"), mcplib.WithString("repo"), mcplib.WithNumber("number"),
		mcplib.WithString("sha"), mcplib.WithString("state"), mcplib.WithString("mine"),
		mcplib.WithNumber("page"), mcplib.WithBoolean("refresh"),
		mcplib.WithString("mode"), mcplib.WithString("task")),
		Handler: func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
			return handlePRTool(ctx, mux, req), nil
		}}}
}

func handlePRTool(ctx context.Context, handler http.Handler, req mcplib.CallToolRequest) *mcplib.CallToolResult {
	action, err := req.RequireString("action")
	if err != nil {
		return mcplib.NewToolResultError("action is required")
	}
	if action == "help" {
		return prToolHelp()
	}
	method, path, body, err := prToolRequest(action, req)
	if err != nil {
		return mcplib.NewToolResultError(err.Error())
	}
	// Only fixed routes are reachable. MCP authenticates the caller; the existing
	// handlers retain session resolution, explicit ownership and REST validation.
	r, err := http.NewRequestWithContext(ctx, method, path, bytes.NewReader(body))
	if err != nil {
		return mcplib.NewToolResultError("invalid PR request")
	}
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code >= http.StatusBadRequest {
		return mcplib.NewToolResultError(fmt.Sprintf("PR request failed (%d): %s", w.Code, strings.TrimSpace(w.Body.String())))
	}
	if w.Code == http.StatusNoContent {
		return internalmcp.ToolResultJSON(map[string]string{"status": "unlinked"})
	}
	var result any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		return mcplib.NewToolResultError("invalid PR response")
	}
	return internalmcp.ToolResultJSON(result)
}
