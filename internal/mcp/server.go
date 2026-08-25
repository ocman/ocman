package mcp

import (
	"net/http"

	mcpserver "github.com/mark3labs/mcp-go/server"
)

// Deps holds the dependencies injected into the MCP server by the
// ocman server package. All fields are required unless noted.
type Deps struct {
	// WorkflowService drives workflow authoring and run-control tools.
	// Optional: nil disables workflow tools.
	WorkflowService workflowService

	// SignFile mints a browser-reachable URL for a file on disk, backing
	// the embed_file tool. Optional: nil makes embed_file report that
	// file embedding is unavailable.
	SignFile FileSigner
}

// Server wraps the mcp-go MCPServer and exposes an http.Handler.
type Server struct {
	handler http.Handler
}

// New constructs a Server from the given dependencies, registers all
// MCP tools, and wraps the result in a StreamableHTTPServer.
func New(deps Deps) *Server {
	s := mcpserver.NewMCPServer(
		"ocman",
		"1.0.0",
		mcpserver.WithToolCapabilities(false),
	)

	addFileTools(s, &fileTools{sign: deps.SignFile})

	addWorkflowTools(s, &workflowTools{svc: deps.WorkflowService})

	httpHandler := mcpserver.NewStreamableHTTPServer(s,
		mcpserver.WithStateLess(true),
	)

	return &Server{handler: httpHandler}
}

// Handler returns the http.Handler for the MCP server. Mount this on
// the ocman HTTP mux at /mcp (with requireLocalhost).
func (s *Server) Handler() http.Handler {
	return s.handler
}

// ServerTools builds the list of server.ServerTool entries for the given
// dependencies. Used by tests that want to register tools into an mcptest
// server without going through the full HTTP transport.
func ServerTools(deps Deps) []mcpserver.ServerTool {
	tools := []mcpserver.ServerTool{
		{Tool: embedFileTool(), Handler: (&fileTools{sign: deps.SignFile}).handleEmbedFile},
	}
	tools = append(tools, workflowServerTools(&workflowTools{svc: deps.WorkflowService})...)
	return tools
}
