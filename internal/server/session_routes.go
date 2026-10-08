package server

import (
	"net/http"
	"strings"
)

// sessionSubPath splits "/api/session/{id}/{rest}" into id and rest.
func sessionSubPath(path, basePrefix string) (id, rest string, ok bool) {
	trimmed := strings.TrimPrefix(path, basePrefix)
	if trimmed == path {
		return "", "", false
	}
	slash := strings.IndexByte(trimmed, '/')
	if slash < 0 {
		return trimmed, "", true
	}
	return trimmed[:slash], trimmed[slash+1:], true
}

// sessionSubRoute describes a single /api/session/... entry.
type sessionSubRoute struct {
	method  string
	pattern string
	handler func(s *Server, w http.ResponseWriter, r *http.Request)
}

// sessionSubRoutes is the canonical registry of every supported
// /api/session/... endpoint. Order matters: more-specific entries
// must come before less-specific ones.
var sessionSubRoutes = []sessionSubRoute{
	// Non-session reserved sub-paths (no {id}).
	{http.MethodPost, "archive", (*Server).handleArchiveSession},
	{http.MethodPost, "seen", (*Server).handleSeenSession},
	{http.MethodPost, "pin", (*Server).handlePinSession},

	// Session-scoped GETs.
	{http.MethodGet, "{id}/agents", (*Server).handleSessionAgents},
	{http.MethodGet, "{id}/commands", (*Server).handleSessionCommands},
	{http.MethodGet, "{id}/models", (*Server).handleSessionModels},
	{http.MethodGet, "{id}/changes", (*Server).handleSessionChanges},
	{http.MethodGet, "{id}/info", (*Server).handleSessionInfo},
	{http.MethodGet, "{id}/permissions", (*Server).handleSessionPermissions},
	{http.MethodGet, "{id}/permission-rules", (*Server).handleSessionPermissionRulesGet},
	{http.MethodPut, "{id}/permission-rules", (*Server).handleSessionPermissionRulesSet},
	{http.MethodGet, "{id}/questions", (*Server).handleSessionQuestions},
	{http.MethodGet, "{id}/events", (*Server).handleSessionEvents},
	{http.MethodGet, "{id}/tasks", (*Server).handleSessionTasks},
	{http.MethodGet, "{id}/auto-approve", (*Server).handleSessionAutoApproveGet},
	{http.MethodGet, "{id}/approved-permissions", (*Server).handleSessionApprovedPermissions},
	{http.MethodGet, "{id}/export.md", (*Server).handleSessionExportMarkdown},
	{http.MethodGet, "{id}/shares", (*Server).handleSessionShares},
	{http.MethodGet, "{id}/queue", (*Server).handleSessionQueueList},

	// Session-scoped POSTs (specific patterns first).
	{http.MethodPost, "{id}/queue/{qmid}/move", (*Server).handleSessionQueueMove},
	{http.MethodDelete, "{id}/queue/{qmid}", (*Server).handleSessionQueueDelete},
	{http.MethodPost, "{id}/questions/{qid}/reject", (*Server).handleSessionQuestion},
	{http.MethodPost, "{id}/questions/{qid}", (*Server).handleSessionQuestion},
	{http.MethodPost, "{id}/permissions/{pid}", (*Server).handleSessionPermission},
	{http.MethodPost, "{id}/auto-approve", (*Server).handleSessionAutoApproveSet},
	{http.MethodPost, "{id}/attachment", (*Server).handleSessionAttachment},
	{http.MethodPost, "{id}/message", (*Server).handleSessionMessage},
	{http.MethodPost, "{id}/restart-opencode", (*Server).handleSessionRestartOpencode},
	{http.MethodPost, "{id}/reload-opencode", (*Server).handleSessionReloadOpencode},
	{http.MethodPost, "{id}/command", (*Server).handleSessionCommand},
	{http.MethodPost, "{id}/shell", (*Server).handleSessionShell},
	{http.MethodPost, "{id}/abort", (*Server).handleSessionAbort},
	{http.MethodPost, "{id}/revert", (*Server).handleSessionRevert},
	{http.MethodPost, "{id}/unrevert", (*Server).handleSessionUnrevert},
	{http.MethodPost, "{id}/compact", (*Server).handleSessionCompact},
	{http.MethodPost, "{id}/fork", (*Server).handleSessionFork},
	{http.MethodPost, "{id}/move", (*Server).handleSessionMove},
	{http.MethodPost, "{id}/share", (*Server).handleCreateSessionShare},
	{http.MethodDelete, "{id}/share/{token}", (*Server).handleRevokeSessionShare},

	// Bare /api/session/{id} (kept last so longer matches win).
	{http.MethodGet, "{id}", (*Server).handleSession},
	{http.MethodPatch, "{id}", (*Server).handleSessionRename},
}

// matchSessionSubRoute matches a path against pattern.
func matchSessionSubRoute(pattern, subpath string) (map[string]string, bool) {
	patSegs := strings.Split(pattern, "/")
	pathSegs := strings.Split(subpath, "/")
	if len(patSegs) != len(pathSegs) {
		return nil, false
	}
	params := make(map[string]string)
	for i, ps := range patSegs {
		if len(ps) >= 2 && ps[0] == '{' && ps[len(ps)-1] == '}' {
			name := ps[1 : len(ps)-1]
			if pathSegs[i] == "" {
				return nil, false
			}
			params[name] = pathSegs[i]
			continue
		}
		if ps != pathSegs[i] {
			return nil, false
		}
	}
	return params, true
}

// dispatchSessionSubpath routes every /api/session/... request via
// the sessionSubRoutes table.
func (s *Server) dispatchSessionSubpath(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/api/session/")

	pathMatched := false
	for _, route := range sessionSubRoutes {
		params, ok := matchSessionSubRoute(route.pattern, trimmed)
		if !ok {
			continue
		}
		pathMatched = true
		if r.Method != route.method {
			continue
		}
		for name, value := range params {
			r.SetPathValue(name, value)
		}
		route.handler(s, w, r)
		return
	}
	if pathMatched {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	http.Error(w, "not found: "+r.URL.Path, http.StatusNotFound)
}
