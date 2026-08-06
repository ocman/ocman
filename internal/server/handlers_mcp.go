package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
)

// mcpHandler returns the shared MCP handler. Both the main mux and the
// dedicated loopback listener serve the same instance, so MCP session
// state doesn't depend on which port a client happened to use.
func (s *Server) mcpHandler() http.Handler {
	s.mcpHandlerOnce.Do(func() { s.mcpHandlerCached = s.buildMCPHandler() })
	return s.mcpHandlerCached
}

// buildMCPHandler constructs the MCP server handler from the Server's
// existing dependencies and returns it as an http.Handler.
//
// The handler is localhost-only (enforced by the caller in StartOnListener).
// It is only registered when the OpenCode platform adapter is present.
func (s *Server) buildMCPHandler() http.Handler {
	deps := internalmcp.Deps{
		OcDB:                  s.db,
		StateDB:               s.stateDB,
		Platform:              s.sessions.Client("opencode"),
		PlatformID:            "opencode",
		EnsureProjectOpencode: internalmcp.ProjectOpencodeEnsurer(s.ensureProjectOpencodePort),
		ChildResults:          s.childResults,
		ChildDisconnected:     s.deferChildResultReconnect,
		SignFile:              s.FileURL,
	}
	if s.stateDB != nil {
		deps.WorkflowService = s.workflowSvc()
	}
	return internalmcp.New(deps).Handler()
}

// mcpServerURL returns the absolute URL of the MCP server endpoint.
// The dedicated loopback listener wins when configured — it's the one
// address that works regardless of password auth. Used in
// /api/capabilities.
func (s *Server) mcpServerURL() string {
	addr := s.mcpAddr
	if addr == "" {
		addr = s.addr
	}
	if addr == "" {
		addr = "localhost:8229"
	}
	// If the address is just a port (":8229"), prepend localhost.
	if len(addr) > 0 && addr[0] == ':' {
		addr = "localhost" + addr
	}
	return fmt.Sprintf("http://%s/mcp", addr)
}

// WithMCPAddr configures the dedicated MCP listener address. Must be
// called before Start. Empty disables the dedicated listener.
func (s *Server) WithMCPAddr(addr string) *Server {
	s.mcpAddr = addr
	return s
}

// startMCPListener serves /mcp on its own loopback-bound listener and
// returns a shutdown func (never nil).
//
// Why a second port: native MCP clients cannot present an auth cookie,
// so this endpoint has to accept the loopback peer as its credential.
// On the main port that would be unsafe — a reverse proxy fronting
// ocman makes every forwarded request look loopback, which would expose
// session-spawning tools to the internet. A separate loopback-bound
// listener is unreachable through that proxy by construction.
//
// Any problem (non-loopback address, port in use) is logged and the
// dedicated listener is skipped: /mcp stays available on the main port
// under the normal auth rules, so this fails closed, never open.
func (s *Server) startMCPListener() func() {
	noop := func() {}
	if s.mcpAddr == "" {
		return noop
	}
	host, _, err := net.SplitHostPort(s.mcpAddr)
	if err != nil {
		log.WithError(err).WithField("addr", s.mcpAddr).Warn("mcp: invalid -mcp-addr, dedicated MCP listener disabled")
		return noop
	}
	if !isLoopbackHostname(strings.Trim(host, "[]")) {
		log.WithField("addr", s.mcpAddr).Warn("mcp: -mcp-addr must be a loopback address (the endpoint is unauthenticated); dedicated MCP listener disabled")
		return noop
	}
	ln, err := net.Listen("tcp", s.mcpAddr)
	if err != nil {
		log.WithError(err).WithField("addr", s.mcpAddr).Warn("mcp: cannot bind -mcp-addr, dedicated MCP listener disabled")
		return noop
	}

	// Resolve the bound address so a ":0" request reports the real port
	// through mcpServerURL / the boot log.
	s.mcpAddr = ln.Addr().String()

	mux := http.NewServeMux()
	// requireLoopbackPeer is belt-and-braces: the listener is already
	// loopback-bound, but it also rejects cross-origin browser requests.
	h := s.requireLoopbackPeer(s.mcpHandler().ServeHTTP)
	mux.HandleFunc("/mcp", h)
	mux.HandleFunc("/mcp/", h)
	srv := newHTTPServer(ln.Addr().String(), withSecurityHeaders(mux))

	go func() {
		log.WithField("addr", ln.Addr().String()).Info("mcp server started")
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Warn("mcp: dedicated listener stopped")
		}
	}()
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
}
