package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/projectsmod"
	"github.com/ocman/ocman/internal/state"
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
		statePlatform := s.sessionStatePlatform(platform)
		if statePlatform != platform {
			if _, ok := s.publicSessionPlatform(platform, sessionID); !ok {
				http.Error(w, "session not found", http.StatusNotFound)
				return "", false
			}
		}
		return statePlatform, true
	}
	p, ok := s.registry.PlatformForSession(r.Context(), sessionID)
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return "", false
	}
	key, _ := s.registry.KeyFor(p)
	return s.sessionStatePlatform(string(key)), true
}

// validateStateRequest is the shared preamble of the session state
// mutation handlers (seen / archive / pin): validate the session ID and
// resolve the owning platform, writing the HTTP error itself on
// failure. Returns the platform ID and whether to proceed.
func (s *Server) validateStateRequest(w http.ResponseWriter, r *http.Request, sessionID, platform string) (string, bool) {
	if !httpx.ValidID(sessionID) {
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
		Interrupted        bool   `json:"interrupted"`
	}
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
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

	if err := s.stateDB.MarkSessionSeen(r.Context(), platform, req.SessionID, req.SessionTimeUpdated, req.Interrupted); err != nil {
		httpx.ServerError(w, "updating seen session state", err)
		return
	}
	s.broadcastSidebarChanged(platform, req.SessionID)

	httpx.WriteJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleArchiveSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Platform           string `json:"platform"`
		SessionID          string `json:"sessionId"`
		SessionTimeUpdated int64  `json:"timeUpdated"`
		Archived           bool   `json:"archived"`
	}
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
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

	if err := s.setSessionArchived(r.Context(), platform, req.SessionID, req.SessionTimeUpdated, req.Archived); err != nil {
		httpx.ServerError(w, "updating archived session state", err)
		return
	}
	httpx.WriteJSON(w, map[string]bool{"ok": true})
}

// setSessionArchived is the shared archive mutation behind the HTTP route and
// the MCP sessions tool. timeUpdated is the session's last update as seen by
// the caller; it is ignored when unarchiving. Either direction supersedes a
// pending deferred archive, in the same step as the write so a deferred
// archive claimed concurrently cannot land after it.
func (s *Server) setSessionArchived(ctx context.Context, platform, sessionID string, timeUpdated int64, archived bool) error {
	platform = s.sessionStatePlatform(platform)
	unlock := s.deferredArchives.lock()
	defer unlock()
	s.deferredArchives.cancelLocked(platform, sessionID)
	return s.writeSessionArchived(ctx, platform, sessionID, timeUpdated, archived)
}

// writeSessionArchived persists and broadcasts archive state. Callers hold
// the deferredArchives lock; it does not take it again.
func (s *Server) writeSessionArchived(ctx context.Context, platform, sessionID string, timeUpdated int64, archived bool) error {
	platform = s.sessionStatePlatform(platform)
	var err error
	if archived {
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
		ts := timeUpdated
		if remoteID, _ := platforms.SplitPlatformID(platform); remoteID == "" {
			if now := time.Now().UnixMilli(); now > ts {
				ts = now
			}
		}
		err = s.stateDB.ArchiveSession(ctx, platform, sessionID, ts)
	} else {
		err = s.stateDB.UnarchiveSession(ctx, platform, sessionID)
	}
	if err != nil {
		return err
	}

	payload, _ := json.Marshal(map[string]any{
		"sessionID": sessionID, "platform": platform,
		"patch": map[string]any{"archived": archived, "snoozedUntil": 0},
	})
	s.broadcastGlobalEvent("ocman.session.changed", payload)
	return nil
}

func (s *Server) handlePinSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Platform  string `json:"platform"`
		SessionID string `json:"sessionId"`
		Pinned    bool   `json:"pinned"`
	}
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
		return
	}
	platform, ok := s.validateStateRequest(w, r, req.SessionID, req.Platform)
	if !ok {
		return
	}

	var err error
	if req.Pinned {
		err = s.stateDB.PinSession(r.Context(), platform, req.SessionID)
	} else {
		err = s.stateDB.UnpinSession(r.Context(), platform, req.SessionID)
	}
	if err != nil {
		httpx.ServerError(w, "updating pinned session state", err)
		return
	}
	s.broadcastSidebarChanged(platform, req.SessionID)

	httpx.WriteJSON(w, map[string]bool{"ok": true})
}

// applySessionState overlays archive/seen/pin flags from state.db onto a
// session slice. Newer activity resurfaces archived sessions according to
// the user's preference: any activity, or only after the session halts.
func (s *Server) applySessionState(ctx context.Context, sessions []db.Session) error {
	return s.applySessionStateWithWrites(ctx, sessions, true)
}

func (s *Server) applySessionStateReadOnly(ctx context.Context, sessions []db.Session) error {
	return s.applySessionStateWithWrites(ctx, sessions, false)
}

func (s *Server) applySessionStateWithWrites(ctx context.Context, sessions []db.Session, write bool) error {
	if err := s.overlayPRWatchTasks(ctx, sessions); err != nil {
		return err
	}
	snoozed, err := s.stateDB.SnoozedSessions(ctx)
	if err != nil {
		return err
	}
	mode, err := s.archiveResurfaceMode(ctx)
	if err != nil {
		return err
	}
	archived, err := s.stateDB.ArchivedSessions(ctx)
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
	archivedProjects, err := s.stateDB.ArchivedProjects(ctx)
	if err != nil {
		return err
	}
	seen, err := s.stateDB.SeenSessionStates(ctx)
	if err != nil {
		return err
	}
	pinned, err := s.stateDB.PinnedSessions(ctx)
	if err != nil {
		return err
	}
	if err := s.sessions.WaitForRoutineCreation(ctx); err != nil {
		return err
	}
	routineSessions, err := s.stateDB.RoutineSessions(ctx)
	if err != nil {
		return err
	}
	factorySessions, err := s.stateDB.FactorySessions(ctx)
	if err != nil {
		return err
	}
	for i := range sessions {
		sessions[i].UnreadCount = 0 // Deprecated wire field, including older peers.
		// Stamp local host identity when an adapter didn't set it
		// (remote adapters stamp their own RemoteID/RemoteName). This
		// gives every local session a host badge of "This machine"
		// while keeping its bare platform key (AD-3/AD-7).
		if sessions[i].RemoteID == "" {
			sessions[i].RemoteID = "local"
			sessions[i].RemoteName = "This machine"
		}

		key := state.Key{Platform: s.sessionStatePlatform(sessions[i].Platform), SessionID: sessions[i].ID}

		seenState, ok := seen[key]
		seenAtUpdate := seenState.TimeUpdated
		if ok {
			sessions[i].SeenTimeUpdated = seenAtUpdate
			if seenAtUpdate >= sessions[i].TimeUpdated && (sessions[i].Status != db.StatusInterrupted || seenState.Interrupted) {
				sessions[i].Seen = true
			}
		}

		if pinnedAt, ok := pinned[key]; ok {
			sessions[i].Pinned = true
			sessions[i].PinnedAt = pinnedAt
		}
		routineID := s.sessionAssociationID(routineSessions, sessions[i].Platform, sessions[i].ID)
		if !isRemotePlatformID(sessions[i].Platform) || routineID != "" {
			sessions[i].RoutineID = routineID
		}
		if sessions[i].RoutineID == "" && sessions[i].ParentID != "" {
			// A subagent of a routine run belongs to the routine too, like
			// factory attempts below.
			sessions[i].RoutineID = s.sessionAssociationID(routineSessions, sessions[i].Platform, sessions[i].ParentID)
		}
		sessions[i].SnoozedUntil = snoozed[key]
		sessions[i].FactoryAttemptID = s.sessionAssociationID(factorySessions, sessions[i].Platform, sessions[i].ID)
		if sessions[i].FactoryAttemptID == "" && sessions[i].ParentID != "" {
			// A subagent of an attempt session belongs to the attempt too, even
			// when its parent row is outside this list.
			sessions[i].FactoryAttemptID = s.sessionAssociationID(factorySessions, sessions[i].Platform, sessions[i].ParentID)
		}

		archivedAtUpdate, ok := archived[key]
		if ok {
			if s.shouldResurfaceSession(ctx, sessions[i], archivedAtUpdate, mode) {
				if write {
					if err := s.stateDB.UnarchiveSession(ctx, key.Platform, key.SessionID); err != nil {
						return err
					}
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
			key := projectsmod.ArchiveKey(sessions[i].RemoteID, sessions[i].Directory)
			if projectArchivedAt, ok := archivedProjects[key]; ok && sessions[i].TimeUpdated <= projectArchivedAt {
				sessions[i].Archived = true
			}
		}
	}

	return nil
}

// applyNotifySessionState overlays seen and archive state without the full
// list's pin, routine or Factory lookups.
func (s *Server) applyNotifySessionState(ctx context.Context, sessions []db.Session) error {
	if err := s.overlayPRWatchTasks(ctx, sessions); err != nil {
		return err
	}
	mode, err := s.archiveResurfaceMode(ctx)
	if err != nil {
		return err
	}
	seen, err := s.stateDB.SeenSessionStates(ctx)
	if err != nil {
		return err
	}
	archived, err := s.stateDB.ArchivedSessions(ctx)
	if err != nil {
		return err
	}
	archivedProjects, err := s.stateDB.ArchivedProjects(ctx)
	if err != nil {
		return err
	}
	for i := range sessions {
		key := state.Key{Platform: s.sessionStatePlatform(sessions[i].Platform), SessionID: sessions[i].ID}

		if seenState, ok := seen[key]; ok {
			seenAtUpdate := seenState.TimeUpdated
			sessions[i].SeenTimeUpdated = seenAtUpdate
			if seenAtUpdate >= sessions[i].TimeUpdated && (sessions[i].Status != db.StatusInterrupted || seenState.Interrupted) {
				sessions[i].Seen = true
			}
		}

		// Use the same resurfacing policy as the full list: notify polls
		// must not unarchive busy sessions behind the sidebar's back.
		if archivedAtUpdate, ok := archived[key]; ok {
			if s.shouldResurfaceSession(ctx, sessions[i], archivedAtUpdate, mode) {
				if err := s.stateDB.UnarchiveSession(ctx, key.Platform, key.SessionID); err != nil {
					return err
				}
			} else {
				sessions[i].Archived = true
			}
		}
		projectKey := projectsmod.ArchiveKey(sessions[i].RemoteID, sessions[i].Directory)
		if archivedAt, ok := archivedProjects[projectKey]; ok && sessions[i].TimeUpdated <= archivedAt {
			sessions[i].Archived = true
		}
	}
	return nil
}
