package server

import (
	"context"
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/remote"
	"github.com/NoUseFreak/ocman/internal/state"
)

// resolvePlatformIDForState returns the platform ID owning a session
// for state.db operations. The caller may also pass an explicit
// platform string in the request body to short-circuit the lookup
// (used when archiving / marking-seen a session whose platform the
// client already knows from its local cache). Empty platform triggers
// a registry reverse lookup.
func (s *Server) resolvePlatformIDForState(w http.ResponseWriter, r *http.Request, sessionID, platform string) (string, bool) {
	if platform != "" {
		if _, ok := s.registry.Get(platforms.ID(platform)); !ok {
			http.Error(w, "unknown platform", http.StatusBadRequest)
			return "", false
		}
		return platform, true
	}
	p, ok := s.registry.PlatformForSession(r.Context(), sessionID)
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return "", false
	}
	return string(p.ID()), true
}

// validateStateRequest is the shared preamble of the session state
// mutation handlers (seen / archive / pin): validate the session ID and
// resolve the owning platform, writing the HTTP error itself on
// failure. Returns the platform ID and whether to proceed.
func (s *Server) validateStateRequest(w http.ResponseWriter, r *http.Request, sessionID, platform string) (string, bool) {
	if !validateID(sessionID) {
		http.Error(w, "invalid session ID", http.StatusBadRequest)
		return "", false
	}
	return s.resolvePlatformIDForState(w, r, sessionID, platform)
}

func (s *Server) handleSeenSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Platform           string `json:"platform"`
		SessionID          string `json:"sessionId"`
		SessionTimeUpdated int64  `json:"timeUpdated"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	if req.SessionTimeUpdated <= 0 {
		http.Error(w, "timeUpdated is required", http.StatusBadRequest)
		return
	}
	platform, ok := s.validateStateRequest(w, r, req.SessionID, req.Platform)
	if !ok {
		return
	}

	if err := s.stateDB.MarkSessionSeen(platform, req.SessionID, req.SessionTimeUpdated); err != nil {
		serverError(w, "updating seen session state", err)
		return
	}

	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleArchiveSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Platform           string `json:"platform"`
		SessionID          string `json:"sessionId"`
		SessionTimeUpdated int64  `json:"timeUpdated"`
		Archived           bool   `json:"archived"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	if req.Archived && req.SessionTimeUpdated <= 0 {
		http.Error(w, "timeUpdated is required", http.StatusBadRequest)
		return
	}
	platform, ok := s.validateStateRequest(w, r, req.SessionID, req.Platform)
	if !ok {
		return
	}

	var err error
	if req.Archived {
		// The client's timeUpdated comes from its cached sidebar row,
		// which can lag the session's real time_updated (the adapter's
		// sessions cache has a multi-second TTL, and a busy session
		// keeps advancing). Storing the stale value verbatim made the
		// next applySessionState pass see TimeUpdated > archivedAtUpdate
		// and immediately auto-unarchive — archived sessions bounced
		// back into the sidebar. Clamp to "now" so only activity
		// strictly after the archive click resurfaces the session.
		//
		// Only clamp for LOCAL sessions: TimeUpdated then shares the
		// hub's clock. For a remote session TimeUpdated originates on the
		// remote's clock; clamping to the hub's now and comparing against
		// a remote-clock TimeUpdated (which can run ahead) auto-unarchives
		// on the next poll/reconnect — the archive never sticks. The
		// client-reported value is the same remote clock the later
		// applySessionState comparison uses, so store it verbatim.
		ts := req.SessionTimeUpdated
		if remoteID, _ := remote.SplitPlatformID(platform); remoteID == "" {
			if now := time.Now().UnixMilli(); now > ts {
				ts = now
			}
		}
		err = s.stateDB.ArchiveSession(platform, req.SessionID, ts)
	} else {
		err = s.stateDB.UnarchiveSession(platform, req.SessionID)
	}
	if err != nil {
		serverError(w, "updating archived session state", err)
		return
	}

	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handlePinSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Platform  string `json:"platform"`
		SessionID string `json:"sessionId"`
		Pinned    bool   `json:"pinned"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	platform, ok := s.validateStateRequest(w, r, req.SessionID, req.Platform)
	if !ok {
		return
	}

	var err error
	if req.Pinned {
		err = s.stateDB.PinSession(platform, req.SessionID)
	} else {
		err = s.stateDB.UnpinSession(platform, req.SessionID)
	}
	if err != nil {
		serverError(w, "updating pinned session state", err)
		return
	}

	writeJSON(w, map[string]bool{"ok": true})
}

// applySessionState overlays archive/seen/pin flags from state.db onto a
// session slice. Auto-unarchives sessions that have been updated since they
// were archived.
func (s *Server) applySessionState(sessions []db.Session) error {
	archived, err := s.stateDB.ArchivedSessions()
	if err != nil {
		return err
	}
	// Archived projects: a session inside an archived project is treated as
	// archived too (keyed by the folded project root), unless the session has
	// activity newer than the project's archived_at — mirroring the project
	// auto-unarchive rule in applyProjectArchiveState. This keeps the redirect
	// target and the sidebar consistent: an archived project's sessions never
	// surface as "active". We only set the display flag here; the project
	// marker is left for handleProjects to auto-unarchive against the full
	// project view.
	archivedProjects, err := s.stateDB.ArchivedProjects()
	if err != nil {
		return err
	}
	seen, err := s.stateDB.SeenSessions()
	if err != nil {
		return err
	}
	pinned, err := s.stateDB.PinnedSessions()
	if err != nil {
		return err
	}
	// Parent links for sessions spawned via the MCP split tools. A
	// split child is a full top-level OpenCode session (no
	// session.parent_id), so the only record of its parent lives in
	// ocman's own child_sessions table. We overlay it here so the UI
	// can nest the child under the session that spawned it.
	childParents, err := s.stateDB.ChildSessionParents()
	if err != nil {
		return err
	}

	// Build the per-platform "I want unread counts for these sessions
	// at this cutoff" maps. Skip sessions that are fully seen
	// (Seen==true) — their count would be zero by definition.
	// Sessions never seen pass cutoff=0 so every message counts.
	unreadCutoffs := map[string]map[string]int64{}

	for i := range sessions {
		// Stamp local host identity when an adapter didn't set it
		// (remote adapters stamp their own RemoteID/RemoteName). This
		// gives every local session a host badge of "This machine"
		// while keeping its bare platform key (AD-3/AD-7).
		if sessions[i].RemoteID == "" {
			sessions[i].RemoteID = "local"
			sessions[i].RemoteName = "This machine"
		}

		key := state.Key{Platform: sessions[i].Platform, SessionID: sessions[i].ID}

		seenAtUpdate, ok := seen[key]
		if ok {
			sessions[i].SeenTimeUpdated = seenAtUpdate
			if seenAtUpdate >= sessions[i].TimeUpdated {
				sessions[i].Seen = true
			}
		}

		if pinnedAt, ok := pinned[key]; ok {
			sessions[i].Pinned = true
			sessions[i].PinnedAt = pinnedAt
		}

		// Overlay the MCP-split parent link, but never override a
		// parent already set by the platform (OpenCode subagent
		// sessions carry their own session.parent_id).
		if sessions[i].ParentID == "" {
			if parentID, ok := childParents[key]; ok {
				sessions[i].ParentID = parentID
			}
		}

		// Queue unread-count lookup for unseen sessions. The
		// cutoff is the user's last-seen time_updated for this
		// session, or 0 (= count every message) when never seen.
		if !sessions[i].Seen {
			byPlatform, ok := unreadCutoffs[sessions[i].Platform]
			if !ok {
				byPlatform = map[string]int64{}
				unreadCutoffs[sessions[i].Platform] = byPlatform
			}
			byPlatform[sessions[i].ID] = seenAtUpdate
		}

		archivedAtUpdate, ok := archived[key]
		if ok {
			if sessions[i].TimeUpdated > archivedAtUpdate {
				if err := s.stateDB.UnarchiveSession(key.Platform, key.SessionID); err != nil {
					return err
				}
			} else {
				sessions[i].Archived = true
				continue
			}
		}

		// Fall back to the session's project archive state. A session in an
		// archived project is archived unless it's been touched since the
		// project was archived (in which case handleProjects will
		// auto-unarchive the whole project on the next projects fetch).
		if len(archivedProjects) > 0 {
			// Per owning host: an archived /repo on another machine says
			// nothing about this session's project.
			key := projectArchiveKey(sessions[i].RemoteID, sessions[i].Directory)
			if projectArchivedAt, ok := archivedProjects[key]; ok && sessions[i].TimeUpdated <= projectArchivedAt {
				sessions[i].Archived = true
			}
		}
	}

	// Resolve unread counts per platform via the optional
	// UnreadCounter interface. Platforms that don't implement it
	// leave UnreadCount at zero; the frontend still shows the
	// "new" affordance via Seen==false.
	if err := s.overlayUnreadCounts(sessions, unreadCutoffs); err != nil {
		return err
	}

	return nil
}

// overlayUnreadCounts asks each platform's optional UnreadCounter for
// counts at the cutoffs collected by applySessionState, then writes
// them onto the matching session entries. Errors from a single
// platform are logged but not fatal — a missing unread count
// degrades gracefully to zero.
func (s *Server) overlayUnreadCounts(sessions []db.Session, cutoffsByPlatform map[string]map[string]int64) error {
	if len(cutoffsByPlatform) == 0 {
		return nil
	}

	// One call per platform, then one O(N) pass to write the
	// numbers back onto the session slice.
	counts := map[state.Key]int{}
	for platformID, cutoffs := range cutoffsByPlatform {
		adapter, ok := s.registry.Get(platforms.ID(platformID))
		if !ok {
			continue
		}
		counter, ok := adapter.(platforms.UnreadCounter)
		if !ok {
			continue
		}
		got, err := counter.UnreadCounts(context.Background(), cutoffs)
		if err != nil {
			log.WithFields(log.Fields{
				"platform": platformID,
				"error":    err,
			}).Warn("UnreadCounts failed; treating as zero")
			continue
		}
		for sid, n := range got {
			counts[state.Key{Platform: platformID, SessionID: sid}] = n
		}
	}

	for i := range sessions {
		key := state.Key{Platform: sessions[i].Platform, SessionID: sessions[i].ID}
		if n, ok := counts[key]; ok {
			sessions[i].UnreadCount = n
		}
	}
	return nil
}
