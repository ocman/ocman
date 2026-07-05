package server

import (
	"fmt"
	"io/fs"
	"net/http"
	"strings"
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
	// hooks) skip the auth layer because isLoopback is strictly
	// stricter than auth.
	mux.HandleFunc("/api/stats", s.get(s.handleStats))
	mux.HandleFunc("/api/metrics", s.get(s.handleMetrics))
	mux.HandleFunc("/api/projects", s.get(s.handleProjects))
	mux.HandleFunc("/api/filesystem/directories", requireGET(requireLocalhost(s.handleFilesystemDirectories)))
	mux.HandleFunc("/api/filesystem/directory-search", requireGET(requireLocalhost(s.handleFilesystemDirectorySearch)))
	mux.HandleFunc("/api/system/stats", s.get(s.handleSystemStats))
	mux.HandleFunc("/api/sessions", s.requireAuth(s.handleSessionsRoot)) // GET = list, POST = create
	mux.HandleFunc("/api/sessions/notify", s.get(s.handleSessionsNotify))
	mux.HandleFunc("/api/events", s.get(s.handleGlobalEvents))
	mux.HandleFunc("/api/session/", s.requireAuth(s.dispatchSessionSubpath))
	// Public, UNAUTHENTICATED share endpoints. A valid share token is
	// the only credential: anyone with the unguessable URL can view the
	// conversation read-only, even when password auth is configured.
	mux.HandleFunc("/api/share/", requireGET(s.handleSharePublic))
	mux.HandleFunc("/api/activity", s.get(s.handleActivity))
	mux.HandleFunc("/api/models", s.get(s.handleModels))
	mux.HandleFunc("/api/hourly", s.get(s.handleHourly))
	mux.HandleFunc("/api/hourly-tokens", s.get(s.handleHourlyTokens))
	mux.HandleFunc("/api/capabilities", s.get(s.handleCapabilities))
	mux.HandleFunc("/api/favorites", s.requireAuth(s.handleFavoritesRoot)) // GET = list, POST = add, DELETE = remove
	mux.HandleFunc("/api/whisper/status", s.get(s.handleWhisperStatus))
	mux.HandleFunc("/api/transcribe", s.post(s.handleTranscribe))
	mux.HandleFunc("/api/cost/calc", s.post(s.handleCalcCost))
	mux.HandleFunc("/api/git/diff", s.get(s.handleGitDiff))
	mux.HandleFunc("/api/git/info", s.get(s.handleGitInfo))
	mux.HandleFunc("/api/git/branches", s.get(s.handleGitBranches))
	mux.HandleFunc("/api/git/checkout", requirePOST(requireLocalhost(s.handleGitCheckout)))
	mux.HandleFunc("/api/tmux/clients", requireGET(requireLocalhost(s.handleTmuxClients)))
	mux.HandleFunc("/api/tmux/sessions", requireGET(requireLocalhost(s.handleTmuxSessions)))
	mux.HandleFunc("/api/tmux/switch", requirePOST(requireLocalhost(s.handleTmuxSwitch)))
	mux.HandleFunc("/api/tmux/launch-opencode", requirePOST(requireLocalhost(s.handleTmuxLaunchOpencode)))
	// Live terminal: WebSocket bridge that attaches an in-app xterm.js
	// terminal to an existing tmux target via a PTY. localhost-only —
	// this is a live shell. The WS upgrade is a GET that hijacks the
	// connection, so it is NOT wrapped in requireGET (that wrapper can
	// interfere with the upgrade).
	mux.HandleFunc("/api/term/ws", requireLocalhost(s.handleTermWS))
	// Terminal-window management (list / create / kill the dedicated
	// `ocman-term-*` windows backing the in-app terminal tabs). Method
	// is dispatched inside the handler (GET/POST/DELETE). localhost-only.
	mux.HandleFunc("/api/term/windows", requireLocalhost(s.handleTermWindows))

	// Worktree endpoints. List + default-base-ref are read-only and
	// safe to expose to authenticated clients; create-and-launch
	// runs `git worktree add` and spawns tmux/opencode, so it's
	// localhost-only like the other launch endpoints.
	mux.HandleFunc("/api/worktree/list", s.get(s.handleWorktreeList))
	mux.HandleFunc("/api/worktree/default-base-ref", s.get(s.handleWorktreeDefaultBaseRef))
	mux.HandleFunc("/api/worktree/create-and-launch", requirePOST(requireLocalhost(s.handleWorktreeCreateAndLaunch)))
	mux.HandleFunc("/api/worktree/remove", requirePOST(requireLocalhost(s.handleWorktreeRemove)))

	// PR/Issue sidebar endpoints — see spec/pr-issue-sidebar/. Read-only
	// proxies to GitHub / Forgejo, scoped to the project at ?dir=<abs>.
	mux.HandleFunc("/api/project/upstreams", s.get(s.handleProjectUpstreams))
	mux.HandleFunc("/api/project/prs", s.get(s.handleProjectPRs))
	mux.HandleFunc("/api/project/issues", s.get(s.handleProjectIssues))
	mux.HandleFunc("/api/project/pr-checks", s.get(s.handleProjectPRChecks))
	mux.HandleFunc("/api/project/forge-user", s.get(s.handleProjectForgeUser))
	// Project archive state (own state.db; no launch), same auth posture
	// as the per-session archive endpoint.
	mux.HandleFunc("/api/project/archive", s.post(s.handleProjectArchive))
	// Launch endpoint: spawns tmux/opencode, so localhost-only like
	// the worktree create-and-launch endpoint.
	mux.HandleFunc("/api/project/handle", requirePOST(requireLocalhost(s.handleProjectHandle)))

	// Agent loops — localhost-only (AD-8). The handler does its own
	// GET/POST dispatch across /api/loops and /api/loops/{id}[/action].
	loopsHandler := requireLocalhost(s.handleLoops)
	mux.HandleFunc("/api/loops", loopsHandler)
	mux.HandleFunc("/api/loops/", loopsHandler)

	// MCP server — localhost-only, enabled by default. Exposes the
	// session-split tools (new_session, etc.)
	// to AI coding agents via the Model Context Protocol.
	mcpHandler := requireLocalhost(s.buildMCPHandler().ServeHTTP)
	mux.HandleFunc("/mcp", mcpHandler)
	mux.HandleFunc("/mcp/", mcpHandler)

	// Auth endpoints. /me is unauthenticated by design (the SPA needs
	// to learn its auth state before it can show the lockscreen).
	// /login and /logout are also unauthenticated — /login is where
	// you prove yourself; /logout just clears a cookie and is
	// idempotent for an already-anonymous client.
	mux.HandleFunc("/api/auth/me", requireGET(s.handleAuthMe))
	mux.HandleFunc("/api/auth/login", requirePOST(s.handleAuthLogin))
	mux.HandleFunc("/api/auth/logout", requirePOST(s.handleAuthLogout))

	// Integration endpoints. These proxy requests to third-party APIs
	// using server-side credentials discovered at startup.
	mux.HandleFunc("/api/integrations/status", s.get(s.handleIntegrationsStatus))
	mux.HandleFunc("/api/integrations/github/preview", s.get(s.handleGitHubPreview))
	mux.HandleFunc("/api/integrations/forgejo/preview", s.get(s.handleForgejoPreview))

	// Settings endpoints — user preferences that must be shared with the
	// backend (e.g. judge prompt sections used by headless auto-approve).
	mux.HandleFunc("/api/settings/prompt-sections", s.requireAuth(s.handlePromptSections))
	mux.HandleFunc("/api/settings/judge-delay", s.requireAuth(s.handleJudgeDelay))
	mux.HandleFunc("/api/settings/judge-model", s.requireAuth(s.handleJudgeModel))
	// Prompt templates for the PR/Issue sidebar's "Handle this" launch
	// action. Stored in state.db's generic `setting` table (schema v12).
	mux.HandleFunc("/api/settings/prompt-templates", s.requireAuth(s.handlePromptTemplates))
	// Master toggle for public session sharing (on by default).
	mux.HandleFunc("/api/settings/sharing", s.requireAuth(s.handleSharingSetting))
	// Global list of active share links, for inspect/revoke in Settings.
	mux.HandleFunc("/api/shares", s.requireAuth(s.get(s.handleAllShares)))
	// Remote-access surface for multi-remote support: this instance's
	// own instance ID + gRPC-listen status, and an explicit token reveal.
	mux.HandleFunc("/api/settings/remote-access", s.get(s.handleRemoteAccess))
	mux.HandleFunc("/api/settings/remote-access/reveal-token", s.post(s.handleRevealRemoteToken))
	// Hub-side remote management (multi-remote support). CRUD + reconnect.
	mux.HandleFunc("/api/remotes", s.requireAuth(s.handleRemotes))
	mux.HandleFunc("/api/remotes/", s.requireAuth(s.handleRemoteByID))
	// New-session machine picker resolver (multi-remote support).
	mux.HandleFunc("/api/sessions/resolve-targets", s.post(s.handleResolveTargets))

	// Best-effort remote-logging sink for the frontend. Localhost-only so
	// it can't be used to flood logs from the network. See
	// handleDebugLog for the JSON shape.
	mux.HandleFunc("/api/debug/log", requirePOST(requireLocalhost(s.handleDebugLog)))

	// Static files with SPA fallback
	staticContent, err := fs.Sub(staticFS, "static")
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
