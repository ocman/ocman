package server

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/gitmod"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/remote"
)

// uploadReadTimeout bounds body reads for the upload handlers (audio,
// composer attachments), which move tens of megabytes and can
// legitimately take much longer on a slow link than httpx.BodyReadTimeout.
var uploadReadTimeout = 5 * time.Minute

// --- Platform dispatch helpers ---

// resolvePlatformForSession returns the Platform adapter owning a
// given session ID. Writes 404 and returns nil if no adapter claims it.
func (s *Server) resolvePlatformForSession(w http.ResponseWriter, r *http.Request, sessionID string) platforms.Platform {
	if !httpx.ValidID(sessionID) {
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
// adapter resolved.
type sessionHandlerFunc func(w http.ResponseWriter, r *http.Request, sessionID string, adapter platforms.Platform)

// withSessionAdapter resolves the route's {id} and its adapter, then calls
// fn. It writes the HTTP error itself when the ID is invalid or unknown.
func (s *Server) withSessionAdapter(w http.ResponseWriter, r *http.Request, fn sessionHandlerFunc) {
	sessionID := r.PathValue("id")
	if adapter := s.resolvePlatformForSession(w, r, sessionID); adapter != nil {
		fn(w, r, sessionID, adapter)
	}
}

// sortAndLimitSessions sorts a combined multi-platform session slice by
// recency (bucketed into 5-minute windows) and truncates to at most limit
// entries. A negative limit truncates nothing; callers validate user input.
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
	if limit >= 0 && len(sessions) > limit {
		return sessions[:limit]
	}
	return sessions
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		newSessionTarget
		Title string `json:"title"`
		// ParentSessionID seeds the new session with that session's
		// permission posture, as /wt does for a new worktree.
		ParentSessionID string `json:"parentSessionId"`
	}
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
		return
	}
	if !req.ResolvePlatform(r.Context(), s.sessionLifecycleModule(), w, "") {
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
	httpx.WriteJSON(w, resp)
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

	httpx.WriteJSON(w, map[string]interface{}{
		"platforms":        out,
		"ownedSessionIds":  s.registry.CanonicalSessionIDs(),
		"hosts":            hosts,
		"worktreeSessions": gitmod.SessionsAvailable(s.registry),
		"mcpServer": map[string]interface{}{
			"enabled": true,
			"url":     s.mcpServerURL(),
		},
	})
}
