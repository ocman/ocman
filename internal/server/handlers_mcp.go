package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
	"github.com/NoUseFreak/ocman/internal/opencodeconfig"
)

// handleMCPConfigStatus reports whether OpenCode's global config
// registers ocman's MCP endpoint, so the UI can offer to install it.
// Never fails the request on a config problem: an unreadable or
// hand-commented config is a status to display, not an error.
func (s *Server) handleMCPConfigStatus(w http.ResponseWriter, _ *http.Request) {
	st, err := opencodeconfig.Check(s.mcpServerURL())
	if err != nil {
		log.WithError(err).Debug("mcp: cannot resolve opencode config path")
		writeJSON(w, map[string]interface{}{
			"configured": false,
			"editable":   false,
			"reason":     err.Error(),
			"wantUrl":    s.mcpServerURL(),
		})
		return
	}
	writeJSON(w, st)
}

// handleMCPConfigInstall writes the ocman MCP entry into OpenCode's
// global config, backing up the original first. Localhost-only: it
// modifies a file in the user's home directory.
func (s *Server) handleMCPConfigInstall(w http.ResponseWriter, _ *http.Request) {
	url := s.mcpServerURL()
	backup, err := opencodeconfig.Install(url)
	if err != nil {
		log.WithError(err).Warn("mcp: installing opencode config failed")
		status := http.StatusInternalServerError
		if errors.Is(err, opencodeconfig.ErrNotEditable) {
			// The config is the user's to fix; not a server fault.
			status = http.StatusConflict
		}
		http.Error(w, err.Error(), status)
		return
	}
	st, err := opencodeconfig.Check(url)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.WithFields(log.Fields{"path": st.Path, "backup": backup, "url": url}).
		Info("mcp: registered ocman in the OpenCode config")
	writeJSON(w, map[string]interface{}{
		"installed":  st.Configured,
		"path":       st.Path,
		"backupPath": backup,
		"url":        url,
	})
}

// mcpHandler returns the shared MCP handler. Both the main mux and the
// dedicated loopback listener serve the same stateless workflow/file tool set.
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
		SignFile: s.FileURL,
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
// privileged workflow and file tools to the internet. A separate loopback-bound
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
