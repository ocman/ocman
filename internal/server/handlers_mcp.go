package server

import (
	"fmt"
	"net/http"

	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode"
	"github.com/NoUseFreak/ocman/internal/tmux"
)

// buildMCPHandler constructs the MCP server handler from the Server's
// existing dependencies and returns it as an http.Handler.
//
// The handler is localhost-only (enforced by the caller in StartOnListener).
// It is only registered when the OpenCode platform adapter is present.
func (s *Server) buildMCPHandler() http.Handler {
	deps := internalmcp.Deps{
		OcDB:         s.db,
		StateDB:      s.stateDB,
		Platform:     s.sessions.Client("opencode"),
		PlatformID:   "opencode",
		LaunchTmux:   internalmcp.TmuxLauncher(tmux.LaunchWorktreeWindow),
		DiscoverPort: internalmcp.PortDiscoverer(opencode.DiscoverOpenCodePortFresh),
	}
	if s.stateDB != nil {
		deps.LoopService = s.loopSvc()
	}
	return internalmcp.New(deps).Handler()
}

// mcpServerURL returns the absolute URL of the MCP server endpoint,
// derived from the server's bind address. Used in /api/capabilities.
func (s *Server) mcpServerURL() string {
	addr := s.addr
	if addr == "" {
		addr = "localhost:8229"
	}
	// If the address is just a port (":8229"), prepend localhost.
	if len(addr) > 0 && addr[0] == ':' {
		addr = "localhost" + addr
	}
	return fmt.Sprintf("http://%s/mcp", addr)
}
