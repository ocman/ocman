package server

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/NoUseFreak/ocman/internal/webui"
)

// routes builds the full HTTP route table: API endpoints plus static
// file serving with SPA fallback. Extracted from StartOnListener so
// the route table is pure data and independently testable.
func (s *Server) routes() (*http.ServeMux, error) {
	mux := http.NewServeMux()

	// API routes — read-only endpoints enforce GET, mutating endpoints
	// enforce POST. Session-scoped routes (/api/session/{id}/...) are
	// dispatched through a single handler because net/http's ServeMux
	// doesn't support path patterns.
	//
	// s.get / s.post compose method + auth checks so the route table
	// stays readable. Routes that are localhost-only (tmux, debug log,
	// hooks) use the localhost guard, which also validates browser origin.
	mux.HandleFunc("/api/projects", s.get(s.handleProjects))
	mux.HandleFunc("/api/system/stats", s.get(s.handleSystemStats))
	mux.HandleFunc("/api/sessions", s.requireAuth(s.handleSessionsRoot)) // GET = list, POST = create
	mux.HandleFunc("/api/sessions/notify", s.get(s.handleSessionsNotify))
	mux.HandleFunc("/api/events", s.get(s.handleGlobalEvents))
	mux.HandleFunc("/api/client-activity", s.post(s.handleClientActivity))
	mux.HandleFunc("/api/session/", s.requireAuth(s.dispatchSessionSubpath))
	// Public, UNAUTHENTICATED share endpoints. A valid share token is
	// the only credential: anyone with the unguessable URL can view the
	// conversation read-only, even when password auth is configured.
	mux.HandleFunc("/api/capabilities", s.get(s.handleCapabilities))
	mux.HandleFunc("/api/doctor", s.get(s.handleDoctor))
	mux.HandleFunc("/api/favorites", s.requireAuth(s.handleFavoritesRoot)) // GET = list, POST = add, DELETE = remove
	mux.HandleFunc("/api/whisper/status", s.get(s.handleWhisperStatus))
	mux.HandleFunc("/api/transcribe", s.post(s.handleTranscribe))
	// Git and worktree endpoints belong to the git module.

	// PR/Issue sidebar endpoints (upstreams, lists, checks, handle) and the
	// prompt templates belong to the project forge module.
	// Project archive state (own state.db; no launch), same auth posture
	// as the per-session archive endpoint.
	mux.HandleFunc("/api/project/archive", s.post(s.handleProjectArchive))
	// Per-project model list (own state.db setting row), same posture.
	mux.HandleFunc("/api/project/settings", s.settingsHandler(s.requireAuth(s.handleProjectSettings)))
	s.mountModules(mux)

	// MCP server — localhost-only, enabled by default.
	// Password auth applies here like everywhere else. Clients that
	// can't send a cookie use the dedicated loopback listener instead
	// (-mcp-addr, see startMCPListener).
	mcpHandler := s.requireLocalhost(s.mcpHandler().ServeHTTP)
	mux.HandleFunc("/mcp", mcpHandler)
	mux.HandleFunc("/mcp/", mcpHandler)

	// Registration of ocman in OpenCode's global config. The install
	// route writes a file in the user's home directory, so it's
	// localhost-only like the other host-control routes.
	mux.HandleFunc("/api/mcp/config", s.get(s.handleMCPConfigStatus))
	mux.HandleFunc("/api/mcp/config/install", s.requireLocalhost(requirePOST(s.handleMCPConfigInstall)))

	// Auth endpoints. /me is unauthenticated by design (the SPA needs
	// to learn its auth state before it can show the lockscreen).
	// /login and /logout are also unauthenticated — /login is where
	// you prove yourself; /logout just clears a cookie and is
	// idempotent for an already-anonymous client.
	mux.HandleFunc("/api/auth/me", requireGET(s.handleAuthMe))
	mux.HandleFunc("/api/auth/login", requirePOST(s.csrfGuard(s.handleAuthLogin)))
	mux.HandleFunc("/api/auth/logout", requirePOST(s.csrfGuard(s.handleAuthLogout)))

	// Settings endpoints — user preferences that must be shared with the
	// backend. The judge settings belong to the auto-approve module.
	mux.HandleFunc("/api/settings/default-agent", s.settingsHandler(s.requireAuth(s.handleDefaultAgent)))
	// Master toggle for public session sharing (on by default).
	// Toggle for worktree sessions inheriting the parent's always-allow
	// permissions at split time (issue #101; on by default).
	mux.HandleFunc("/api/settings/worktree-inherit-permissions", s.settingsHandler(s.requireAuth(s.handleWorktreeInheritPermissions)))
	mux.HandleFunc("/api/settings/auto-archive", s.settingsHandler(s.requireAuth(s.handleAutoArchiveSettings)))
	mux.HandleFunc("/api/settings/archive-resurface", s.settingsHandler(s.requireAuth(s.handleArchiveResurface)))
	mux.HandleFunc("/api/settings/model-fallthrough", s.settingsHandler(s.requireAuth(s.handleModelFallthroughSettings)))
	// Global list of active share links, for inspect/revoke in Settings.
	// Remote-access surface for multi-remote support: this instance's
	// own instance ID + gRPC-listen status, and an explicit token reveal.
	mux.HandleFunc("/api/settings/remote-access", s.get(s.handleRemoteAccess))
	mux.HandleFunc("/api/settings/remote-access/reveal-token", s.post(s.requireLocalhost(s.handleRevealRemoteToken)))
	// Hub-side remote management (multi-remote support). CRUD + reconnect.
	mux.HandleFunc("/api/remotes", s.requireAuth(s.handleRemotes))
	mux.HandleFunc("/api/remotes/", s.requireAuth(s.handleRemoteByID))
	// New-session machine picker resolver (multi-remote support).
	mux.HandleFunc("/api/sessions/resolve-targets", s.post(s.handleResolveTargets))
	mux.HandleFunc("/api/sessions/prepare", s.post(s.requireLocalhost(s.handlePrepareSession)))
	mux.HandleFunc("/api/sessions/start", s.post(s.requireLocalhost(s.handleStartSession)))

	// Best-effort remote-logging sink for the frontend. Localhost-only so
	// it can't be used to flood logs from the network. See
	// handleDebugLog for the JSON shape.
	mux.HandleFunc("/api/debug/log", requirePOST(s.requireLocalhost(s.handleDebugLog)))

	// Static files with SPA fallback
	staticContent, err := webui.FS()
	if err != nil {
		return nil, fmt.Errorf("failed to get static subtree: %w", err)
	}
	fileServer := http.FileServer(http.FS(staticContent))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
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
