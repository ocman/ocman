package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// maxRequestBody is the maximum allowed request body size (1 MB).
const maxRequestBody = 1 << 20

// maxSendMessageBody is the maximum allowed body for send-message
// (20 MB to support inline images).
const maxSendMessageBody = 20 << 20

// maxAudioUpload is the maximum allowed audio upload size (25 MB).
const maxAudioUpload = 25 << 20

// validIDPattern matches safe session/resource IDs (alphanumeric, hyphens, underscores).
var validIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// validateID checks that an ID is safe for use in URLs and log messages.
func validateID(id string) bool {
	return id != "" && len(id) <= 256 && validIDPattern.MatchString(id)
}

// readAndUnmarshal reads the request body (up to maxBytes) and unmarshals
// it into dst. Returns false and writes an HTTP error if reading or
// parsing fails.
func readAndUnmarshal(w http.ResponseWriter, r *http.Request, maxBytes int64, dst interface{}) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBytes))
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return false
	}
	if err := json.Unmarshal(body, dst); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return false
	}
	return true
}

// --- Platform dispatch helpers ---

// resolvePlatformForSession returns the Platform adapter owning a
// given session ID. Writes 404 and returns nil if no adapter claims it.
func (s *Server) resolvePlatformForSession(w http.ResponseWriter, r *http.Request, sessionID string) platforms.Platform {
	if !validateID(sessionID) {
		http.Error(w, "invalid session ID", http.StatusBadRequest)
		return nil
	}
	// Honour an explicit ?platform= first (AD-2b): two hosts may have the
	// same session_id, so a remote session must be addressed by its
	// compound platform key to avoid mis-routing. Falls back to the
	// session-id reverse lookup for local / legacy URLs that omit it.
	if plat := strings.TrimSpace(r.URL.Query().Get("platform")); plat != "" {
		if p, ok := s.registry.Get(platforms.ID(plat)); ok {
			return p
		}
		http.Error(w, "session not found", http.StatusNotFound)
		return nil
	}
	p, ok := s.registry.PlatformForSession(r.Context(), sessionID)
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return nil
	}
	return p
}

// sessionHandlerFunc is a handler that already has the session ID and
// adapter resolved. rest is the URL segment after the session ID (empty
// for bare /{id} routes).
type sessionHandlerFunc func(w http.ResponseWriter, r *http.Request, sessionID, rest string, adapter platforms.Platform)

// withSessionAdapter extracts the session ID and adapter from the request
// and calls fn. It writes appropriate HTTP errors and returns early when
// the session ID is missing or unknown, eliminating the repeated 5-line
// preamble across all session-scoped handlers.
func (s *Server) withSessionAdapter(w http.ResponseWriter, r *http.Request, fn sessionHandlerFunc) {
	sessionID, rest, ok := sessionSubPath(r.URL.Path, "/api/session/")
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	adapter := s.resolvePlatformForSession(w, r, sessionID)
	if adapter == nil {
		return
	}
	fn(w, r, sessionID, rest, adapter)
}

// writePlatformError maps a Platform error to an appropriate HTTP response.
func writePlatformError(w http.ResponseWriter, msg string, err error) {
	if errors.Is(err, platforms.ErrUnsupported) {
		http.Error(w, "operation not supported by this platform", http.StatusNotImplemented)
		return
	}
	if errors.Is(err, platforms.ErrNotFound) {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	if errors.Is(err, platforms.ErrBusy) {
		http.Error(w, "session is currently processing a prompt; try again in a moment", http.StatusConflict)
		return
	}
	if errors.Is(err, platforms.ErrPlatformUnreachable) {
		http.Error(w, "no running platform instance for this location", http.StatusServiceUnavailable)
		return
	}
	if errors.Is(err, platforms.ErrUpstreamRejected) {
		log.WithError(err).Warn(msg)
		var ue *platforms.UpstreamError
		body := "the platform rejected the request"
		if errors.As(err, &ue) && ue.Message != "" {
			body = ue.Message
		}
		http.Error(w, body, http.StatusUnprocessableEntity)
		return
	}
	log.WithError(err).Error(msg)
	http.Error(w, "failed to reach platform instance", http.StatusBadGateway)
}

// requireDB returns true if s.db is available, or writes a 501 error
// and returns false.
func (s *Server) requireDB(w http.ResponseWriter) bool {
	if s.db == nil {
		http.Error(w, "OpenCode platform is not enabled", http.StatusNotImplemented)
		return false
	}
	return true
}

// --- Query param helpers ---

// parseIntParam reads an integer query parameter by name. Returns
// fallback when the parameter is absent, empty, or not a valid integer.
func parseIntParam(r *http.Request, name string, fallback int) int {
	v := strings.TrimSpace(r.URL.Query().Get(name))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

// parseInt64Param is like parseIntParam but returns int64. Used for
// millisecond timestamps that overflow int on 32-bit platforms.
func parseInt64Param(r *http.Request, name string, fallback int64) int64 {
	v := strings.TrimSpace(r.URL.Query().Get(name))
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return fallback
	}
	return n
}

// parseSinceParam reads the ?days= query param and returns a Unix
// millisecond cutoff. Returns 0 (no filter) when the param is absent
// or zero.
func parseSinceParam(r *http.Request) int64 {
	if v := strings.TrimSpace(r.URL.Query().Get("days")); v != "" && v != "0" {
		dayCount, err := strconv.ParseInt(v, 10, 64)
		if err == nil && dayCount > 0 {
			return time.Now().Add(-time.Duration(dayCount) * 24 * time.Hour).UnixMilli()
		}
	}
	return 0
}

// normaliseDirParam trims surrounding whitespace and a single trailing slash
// from a directory-prefix filter.
func normaliseDirParam(raw string) string {
	dir := strings.TrimSpace(raw)
	if dir == "" {
		return ""
	}
	if strings.HasSuffix(dir, "/") && dir != "/" {
		dir = dir[:len(dir)-1]
	}
	return dir
}

// sortAndLimitSessions sorts a combined multi-platform session slice by
// recency (bucketed into 5-minute windows) and truncates to at most limit entries.
func sortAndLimitSessions(sessions []db.Session, limit int) []db.Session {
	const bucketMs = 5 * 60 * 1000
	sort.SliceStable(sessions, func(i, j int) bool {
		bi, bj := sessions[i].TimeUpdated/bucketMs, sessions[j].TimeUpdated/bucketMs
		if bi != bj {
			return bi > bj
		}
		if sessions[i].ProjectID != sessions[j].ProjectID {
			return sessions[i].ProjectID < sessions[j].ProjectID
		}
		return sessions[i].Title < sessions[j].Title
	})
	if len(sessions) > limit {
		return sessions[:limit]
	}
	return sessions
}

// --- Route dispatcher ---

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
	{http.MethodGet, "{id}/questions", (*Server).handleSessionQuestions},
	{http.MethodGet, "{id}/events", (*Server).handleSessionEvents},
	{http.MethodGet, "{id}/tasks", (*Server).handleSessionTasks},
	{http.MethodGet, "{id}/auto-approve", (*Server).handleSessionAutoApproveGet},
	{http.MethodGet, "{id}/approved-permissions", (*Server).handleSessionApprovedPermissions},
	{http.MethodGet, "{id}/export.md", (*Server).handleSessionExportMarkdown},
	{http.MethodGet, "{id}/shares", (*Server).handleSessionShares},

	// Session-scoped POSTs (specific patterns first).
	{http.MethodPost, "{id}/questions/{qid}/reject", (*Server).handleSessionQuestion},
	{http.MethodPost, "{id}/questions/{qid}", (*Server).handleSessionQuestion},
	{http.MethodPost, "{id}/permissions/{pid}", (*Server).handleSessionPermission},
	{http.MethodPost, "{id}/auto-approve", (*Server).handleSessionAutoApproveSet},
	{http.MethodPost, "{id}/attachment", (*Server).handleSessionAttachment},
	{http.MethodPost, "{id}/message", (*Server).handleSessionMessage},
	{http.MethodPost, "{id}/command", (*Server).handleSessionCommand},
	{http.MethodPost, "{id}/shell", (*Server).handleSessionShell},
	{http.MethodPost, "{id}/abort", (*Server).handleSessionAbort},
	{http.MethodPost, "{id}/compact", (*Server).handleSessionCompact},
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
		if _, ok := matchSessionSubRoute(route.pattern, trimmed); !ok {
			continue
		}
		pathMatched = true
		if r.Method != route.method {
			continue
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

// --- /api/sessions (GET = list, POST = create) ---

func (s *Server) handleSessionsRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleSessions(w, r)
	case http.MethodPost:
		s.handleCreateSession(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Platform  string `json:"platform"`
		Directory string `json:"directory"`
		Title     string `json:"title"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	if req.Directory == "" {
		http.Error(w, "directory is required", http.StatusBadRequest)
		return
	}

	var adapter platforms.Platform
	if req.Platform != "" {
		p, ok := s.registry.Get(platforms.ID(req.Platform))
		if !ok {
			http.Error(w, "unknown platform", http.StatusBadRequest)
			return
		}
		adapter = p
	} else {
		for _, p := range s.registry.Platforms() {
			if !p.Available(r.Context()) {
				continue
			}
			if adapter != nil {
				http.Error(w, "multiple platforms available — specify ?platform=<id>", http.StatusBadRequest)
				return
			}
			adapter = p
		}
	}
	if adapter == nil {
		http.Error(w, "no platform available to create a session", http.StatusServiceUnavailable)
		return
	}

	resp, err := adapter.CreateSession(r.Context(), platforms.CreateSessionRequest{
		Directory: req.Directory,
		Title:     req.Title,
	})
	if err != nil {
		writePlatformError(w, "creating session", err)
		return
	}
	writeJSON(w, resp)
}

// --- Capabilities endpoint ---

type capabilityEntry struct {
	ID           string                 `json:"id"`
	DisplayName  string                 `json:"displayName"`
	Available    bool                   `json:"available"`
	Capabilities platforms.Capabilities `json:"capabilities"`
	// RemoteID / RemoteName are present only for remote platforms so the
	// frontend can show a host badge without parsing the compound ID.
	RemoteID   string `json:"remoteId,omitempty"`
	RemoteName string `json:"remoteName,omitempty"`
}

// hostCapabilityEntry surfaces a machine's directory-scoped host
// capabilities (AD-16/AD-17). Additive alongside the existing
// platform-scoped entries; the frontend gates host UI on these flags.
type hostCapabilityEntry struct {
	RemoteID     string           `json:"remoteId"`
	RemoteName   string           `json:"remoteName"`
	Capabilities hostsvc.HostCaps `json:"capabilities"`
}

func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out := make([]capabilityEntry, 0)
	for _, p := range s.registry.Platforms() {
		out = append(out, capabilityEntry{
			ID:           string(p.ID()),
			DisplayName:  p.DisplayName(),
			Available:    p.Available(ctx),
			Capabilities: p.Capabilities(),
		})
	}

	// Host capabilities, grouped per machine. v1 surfaces the local
	// machine; remote hosts are appended once registered (Phase 6).
	hosts := []hostCapabilityEntry{{
		RemoteID:     "local",
		RemoteName:   "This machine",
		Capabilities: s.hostCaps(),
	}}
	for id, h := range s.router().Remotes() {
		hosts = append(hosts, hostCapabilityEntry{
			RemoteID:     id,
			RemoteName:   id,
			Capabilities: h.Capabilities(),
		})
	}

	writeJSON(w, map[string]interface{}{
		"platforms":        out,
		"hosts":            hosts,
		"worktreeSessions": worktreeSessionsAvailable(s.registry),
		"mcpServer": map[string]interface{}{
			"enabled": true,
			"url":     s.mcpServerURL(),
		},
	})
}
