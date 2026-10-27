package mcp

import (
	"context"
	"maps"
	"net/http"

	"github.com/NoUseFreak/ocman/internal/platforms"
	mcplib "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// Deps holds the dependencies injected into the MCP server by the
// ocman server package. All fields are required unless noted.
type Deps struct {
	// ToolMiddleware applies public transport identity/security boundaries.
	ToolMiddleware mcpserver.ToolHandlerMiddleware
	// Tools are contributed by feature modules (module.ToolProvider).
	Tools []mcpserver.ServerTool
	// Optional: nil disables session tools. The create and send actions also
	// need the service to implement CreateSession and SendSessionMessage.
	SessionService sessionService
	// Optional: reads an existing instance's directory catalog, without launching it.
	ModelCatalog func(context.Context, string, platforms.DirectoryCatalogRequest) (*platforms.DirectoryCatalog, string, error)
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
	if deps.ToolMiddleware != nil {
		s.Use(deps.ToolMiddleware)
	}

	s.AddTools(ServerTools(deps)...)

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
	tools = append(tools, deps.Tools...)
	tools = append(tools, sessionServerTools(&sessionTools{svc: deps.SessionService})...)
	tools = append(tools, modelServerTools(deps)...)
	for i := range tools {
		tools[i].Tool.InputSchema.Properties = maps.Clone(tools[i].Tool.InputSchema.Properties)
		mcplib.WithString("intent", mcplib.Description(
			"Briefly explain what this call achieves for the user's task. Include on every call, including help and read-only calls. Shown in the conversation; does not change the operation. In Code Mode, use an inline string literal so it can be displayed. Optional for backward compatibility.",
		))(&tools[i].Tool)
	}
	return tools
}
