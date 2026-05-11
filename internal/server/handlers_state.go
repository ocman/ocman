package server

import (
	"net/http"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
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

func (s *Server) handleSeenSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Platform           string `json:"platform"`
		SessionID          string `json:"sessionId"`
		SessionTimeUpdated int64  `json:"timeUpdated"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	if !validateID(req.SessionID) {
		http.Error(w, "invalid session ID", http.StatusBadRequest)
		return
	}
	if req.SessionTimeUpdated <= 0 {
		http.Error(w, "timeUpdated is required", http.StatusBadRequest)
		return
	}
	platform, ok := s.resolvePlatformIDForState(w, r, req.SessionID, req.Platform)
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
	if !validateID(req.SessionID) {
		http.Error(w, "invalid session ID", http.StatusBadRequest)
		return
	}
	if req.Archived && req.SessionTimeUpdated <= 0 {
		http.Error(w, "timeUpdated is required", http.StatusBadRequest)
		return
	}
	platform, ok := s.resolvePlatformIDForState(w, r, req.SessionID, req.Platform)
	if !ok {
		return
	}

	var err error
	if req.Archived {
		err = s.stateDB.ArchiveSession(platform, req.SessionID, req.SessionTimeUpdated)
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
	if !validateID(req.SessionID) {
		http.Error(w, "invalid session ID", http.StatusBadRequest)
		return
	}
	platform, ok := s.resolvePlatformIDForState(w, r, req.SessionID, req.Platform)
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
	seen, err := s.stateDB.SeenSessions()
	if err != nil {
		return err
	}
	pinned, err := s.stateDB.PinnedSessions()
	if err != nil {
		return err
	}

	for i := range sessions {
		key := state.Key{Platform: sessions[i].Platform, SessionID: sessions[i].ID}

		seenAtUpdate, ok := seen[key]
		if ok && seenAtUpdate >= sessions[i].TimeUpdated {
			sessions[i].Seen = true
		}

		if pinnedAt, ok := pinned[key]; ok {
			sessions[i].Pinned = true
			sessions[i].PinnedAt = pinnedAt
		}

		archivedAtUpdate, ok := archived[key]
		if !ok {
			continue
		}
		if sessions[i].TimeUpdated > archivedAtUpdate {
			if err := s.stateDB.UnarchiveSession(key.Platform, key.SessionID); err != nil {
				return err
			}
			continue
		}
		sessions[i].Archived = true
	}

	return nil
}
