package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/remote"
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

// bodyReadTimeout bounds how long a handler will wait for a bounded,
// fully-buffered request body. Applied per handler rather than as
// http.Server.ReadTimeout, which would also cut off SSE streams,
// WebSocket terminals, and multi-megabyte uploads. A var so tests can
// shrink it.
var bodyReadTimeout = 30 * time.Second

// uploadReadTimeout is the same bound for the upload handlers (audio,
// composer attachments), which move tens of megabytes and can
// legitimately take much longer on a slow link.
var uploadReadTimeout = 5 * time.Minute

// setBodyReadDeadline bounds the time spent reading this request's body,
// so a client that trickles bytes can't pin a goroutine indefinitely.
// No-op when the ResponseWriter doesn't support it (e.g. httptest
// recorders), which is why the error is dropped.
func setBodyReadDeadline(w http.ResponseWriter, d time.Duration) {
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(d))
}

// clearBodyReadDeadline drops the deadline once the body is in hand, so
// a handler that then does slow work (git, tmux, an upstream agent call)
// isn't affected by a leftover deadline on the connection.
func clearBodyReadDeadline(w http.ResponseWriter) {
	_ = http.NewResponseController(w).SetReadDeadline(time.Time{})
}

// readAndUnmarshal reads the request body (up to maxBytes) and unmarshals
// it into dst. Returns false and writes an HTTP error if reading or
// parsing fails.
func readAndUnmarshal(w http.ResponseWriter, r *http.Request, maxBytes int64, dst interface{}) bool {
	setBodyReadDeadline(w, bodyReadTimeout)
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBytes))
	if err != nil {
		// Deliberately leave the (expired) deadline in place: clearing
		// it here would make net/http block forever draining the body
		// the client never finished sending.
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return false
	}
	clearBodyReadDeadline(w)
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

// withSessionPath extracts and validates the session ID from the URL
// without resolving the adapter. Mutation handlers use it and delegate
// adapter resolution to the session service.
func (s *Server) withSessionPath(w http.ResponseWriter, r *http.Request, fn func(w http.ResponseWriter, r *http.Request, sessionID, rest string)) {
	sessionID, rest, ok := sessionSubPath(r.URL.Path, "/api/session/")
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !validateID(sessionID) {
		http.Error(w, "invalid session ID", http.StatusBadRequest)
		return
	}
	fn(w, r, sessionID, rest)
}

// platformHint returns the explicit ?platform= query value, if any
// (AD-2b: remote sessions are addressed by their compound platform key).
func platformHint(r *http.Request) string {
	return strings.TrimSpace(r.URL.Query().Get("platform"))
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

// parseAbsDir reads the required ?dir= query parameter and validates
// that it is an absolute path, writing the HTTP error itself on
// failure. Returns the directory and whether to proceed.
func parseAbsDir(w http.ResponseWriter, r *http.Request) (string, bool) {
	dir := normaliseDirParam(r.URL.Query().Get("dir"))
	if dir == "" {
		http.Error(w, "dir query parameter is required", http.StatusBadRequest)
		return "", false
	}
	if !filepath.IsAbs(dir) {
		http.Error(w, "dir must be an absolute path", http.StatusBadRequest)
		return "", false
	}
	return dir, true
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
		// ParentSessionID seeds the new session with that session's
		// permission posture, as /wt does for a new worktree.
		ParentSessionID string `json:"parentSessionId"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	log.WithFields(log.Fields{
		"platform":  req.Platform,
		"directory": req.Directory,
	}).Info("hub: create session request")
	var port string
	if req.Directory != "" {
		var ok bool
		if _, port, ok = s.ensureProjectForCreate(w, r, req.Platform, req.Directory); !ok {
			return
		}
	}
	resp, err := s.sessions.Create(r.Context(), req.Platform, platforms.CreateSessionRequest{
		Directory: req.Directory,
		Title:     req.Title,
		Port:      port,
	})
	if err != nil {
		log.WithError(err).WithFields(log.Fields{
			"platform":  req.Platform,
			"directory": req.Directory,
		}).Warn("hub: create session failed")
		writeSessionSvcError(w, "creating session", err)
		return
	}
	// Soft-fail like /wt: inheritance never turns a created session into an error.
	s.applyInheritedPermissions(r, s.buildInheritedPermissions(r, req.Platform, req.ParentSessionID), resp.ID)
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
	RemoteID         string           `json:"remoteId"`
	RemoteName       string           `json:"remoteName"`
	Capabilities     hostsvc.HostCaps `json:"capabilities"`
	PluginManagement bool             `json:"pluginManagement"`
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
		RemoteID:         "local",
		RemoteName:       "This machine",
		Capabilities:     s.hostCaps(),
		PluginManagement: s.stateDB != nil,
	}}
	pluginCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	for id, h := range s.router().Remotes() {
		available := s.routePluginOperation(pluginCtx, id, remote.PluginRequest{Operation: "available"})
		hosts = append(hosts, hostCapabilityEntry{
			RemoteID:         id,
			RemoteName:       id,
			Capabilities:     h.Capabilities(),
			PluginManagement: available.Error == nil && string(available.Value) == "true",
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
