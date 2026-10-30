package server

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/ocman/ocman/internal/webui"
)

// routes builds the full HTTP route table: API endpoints plus static
// file serving with SPA fallback. Extracted from StartOnListener so
// the route table is pure data and independently testable.
//
// API routes use method patterns ("GET /api/projects"), so the mux
// answers a wrong method with 405 and an Allow header. They live on their
// own mux: the SPA fallback matches every path, and sharing a mux with it
// would turn a GET to a POST-only route into the SPA's 404.
func (s *Server) routes() (*http.ServeMux, error) {
	api := http.NewServeMux()

	// Routes that are localhost-only (tmux, debug log, hooks) use the
	// localhost guard, which also validates browser origin.
	auth := s.requireAuth
	setting := func(pattern string, h http.HandlerFunc) { api.HandleFunc(pattern, s.settingsHandler(auth(h))) }
	api.HandleFunc("GET /api/projects", auth(s.handleProjects))
	api.HandleFunc("GET /api/system/stats", auth(s.handleSystemStats))
	api.HandleFunc("GET /api/session-identity", auth(s.handleSessionIdentity))
	api.HandleFunc("GET /api/sessions", auth(s.handleSessions))
	api.HandleFunc("POST /api/sessions", auth(s.handleCreateSession))
	api.HandleFunc("GET /api/sessions/notify", auth(s.handleSessionsNotify))
	api.HandleFunc("GET /api/events", auth(s.handleGlobalEvents))
	api.HandleFunc("POST /api/client-activity", auth(s.handleClientActivity))
	s.mountSessionRoutes(api)
	api.HandleFunc("GET /api/capabilities", auth(s.handleCapabilities))
	api.HandleFunc("GET /api/doctor", auth(s.handleDoctor))
	api.HandleFunc("GET /api/provider-accounts", s.requireLocalhost(s.handleProviderAccounts))
	api.HandleFunc("GET /api/hook-plugin", s.requireLocalhost(s.handleHookPlugin))
	api.HandleFunc("POST /api/hook-plugin/install", s.requireLocalhost(s.handleHookPlugin))
	api.HandleFunc("POST /api/hook-plugin/uninstall", s.requireLocalhost(s.handleHookPlugin))
	api.HandleFunc("GET /api/favorites", auth(s.handleListFavorites))
	api.HandleFunc("POST /api/favorites", auth(s.handleAddFavorite))
	api.HandleFunc("DELETE /api/favorites", auth(s.handleRemoveFavorite))
	// Git and worktree endpoints belong to the git module.

	// PR/Issue sidebar endpoints (upstreams, lists, checks, handle) and the
	// prompt templates belong to the project forge module.
	// Project archive state (own state.db; no launch), same auth posture
	// as the per-session archive endpoint.
	api.HandleFunc("POST /api/project/archive", auth(s.handleProjectArchive))
	api.HandleFunc("POST /api/project/merge", auth(s.handleProjectMerge))
	// Per-project model list (own state.db setting row), same posture.
	api.HandleFunc("/api/project/settings", s.settingsHandler(auth(s.handleProjectSettings)))
	s.mountModules(api)

	// Registration of ocman in OpenCode's global config. The install
	// route writes a file in the user's home directory, so it's
	// localhost-only like the other host-control routes.
	api.HandleFunc("GET /api/mcp/config", auth(s.handleMCPConfigStatus))
	api.HandleFunc("POST /api/mcp/config/install", s.requireLocalhost(s.handleMCPConfigInstall))

	// Auth endpoints. /me is unauthenticated by design (the SPA needs
	// to learn its auth state before it can show the lockscreen).
	// /login and /logout are also unauthenticated — /login is where
	// you prove yourself; /logout just clears a cookie and is
	// idempotent for an already-anonymous client.
	api.HandleFunc("GET /api/auth/me", s.handleAuthMe)
	api.HandleFunc("POST /api/auth/login", s.csrfGuard(s.handleAuthLogin))
	api.HandleFunc("POST /api/auth/logout", s.csrfGuard(s.handleAuthLogout))

	// Settings endpoints — user preferences that must be shared with the
	// backend. The judge settings belong to the auto-approve module.
	s.defaultAgentSetting().Mount(setting, "/api/settings/default-agent")
	s.betaFeaturesSetting().Mount(setting, "/api/settings/beta-features")
	// Toggle for worktree sessions inheriting the parent's always-allow
	// permissions at split time (issue #101; on by default).
	s.worktreeInheritSetting().Mount(setting, "/api/settings/worktree-inherit-permissions")
	s.autoArchiveSetting().Mount(setting, "/api/settings/auto-archive")
	s.archiveResurfaceSetting().Mount(setting, "/api/settings/archive-resurface")
	// New-session machine picker resolver (multi-remote support).
	api.HandleFunc("POST /api/sessions/resolve-targets", auth(s.handleResolveTargets))

	// Best-effort remote-logging sink for the frontend. Localhost-only so
	// it can't be used to flood logs from the network. See
	// handleDebugLog for the JSON shape.
	api.HandleFunc("POST /api/debug/log", s.requireLocalhost(s.handleDebugLog))

	mux := http.NewServeMux()
	mux.Handle("/api/", api)

	// MCP server — localhost-only, enabled by default.
	// Password auth applies here like everywhere else. Clients that
	// can't send a cookie use the dedicated loopback listener instead
	// (-mcp-addr, see startMCPListener).
	mcpHandler := s.requireLocalhost(s.mcpHandler().ServeHTTP)
	mux.HandleFunc("/mcp", mcpHandler)
	mux.HandleFunc("/mcp/", mcpHandler)

	// Static files with SPA fallback
	staticContent, err := webui.FS()
	if err != nil {
		return nil, fmt.Errorf("failed to get static subtree: %w", err)
	}
	fileServer := http.FileServer(http.FS(staticContent))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Try to serve the file directly
		path := r.URL.Path
		if path == "/" {
			fileServer.ServeHTTP(w, r)
			return
		}
		// Check if the file exists in static
		f, err := staticContent.Open(strings.TrimPrefix(path, "/"))
		if err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		// SPA fallback: serve index.html for client-side routes
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})

	return mux, nil
}
