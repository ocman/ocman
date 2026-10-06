package server

import (
	"context"
	"encoding/json"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode"
	"github.com/NoUseFreak/ocman/internal/sessionsvc"
)

// broadcastSessionIdle updates bell/favicon indicators without a notify poll.
func (s *Server) broadcastSessionIdle(sessionID string) {
	if sessionID == "" {
		return
	}
	payload, err := json.Marshal(map[string]interface{}{"sessionID": sessionID})
	if err == nil {
		s.broadcastGlobalEvent("ocman.session.idle", payload)
	}
}

// broadcastSessionChanged refreshes the list for upstream session changes.
func (s *Server) broadcastSessionChanged(sessionID string) {
	if sessionID == "" {
		return
	}
	payload, err := json.Marshal(map[string]interface{}{"sessionID": sessionID})
	if err != nil {
		return
	}
	s.broadcastGlobalEvent("ocman.session.changed", payload)
	// Recovery for OpenCode terminal changes without a matching idle edge.
	s.replyToConversation(context.Background(), "opencode", sessionID)
}

func (s *Server) broadcastSessionStatus(sessionID string, status db.SessionStatus) {
	s.broadcastSessionPatch(sessionID, map[string]interface{}{"status": status})
}

func (s *Server) broadcastSessionTitle(sessionID, title string) {
	s.broadcastSessionPatch(sessionID, map[string]interface{}{"title": title})
}

func (s *Server) broadcastSessionPatch(sessionID string, patch map[string]interface{}) {
	if sessionID == "" {
		return
	}
	payload, err := json.Marshal(map[string]interface{}{"sessionID": sessionID, "platform": opencode.PlatformID, "patch": patch})
	if err == nil {
		s.broadcastGlobalEvent("ocman.session.changed", payload)
	}
}

// broadcastSessionCreated seeds a provisional row without extra I/O. A list
// refetch replaces its default waiting status with authoritative state.
func (s *Server) broadcastSessionCreated(info sessionsvc.CreatedSession) {
	if info.ID == "" {
		return
	}
	now := time.Now().UnixMilli()
	session := db.Session{
		ID: info.ID, Platform: info.Platform, Directory: info.Directory, Title: info.Title,
		TimeCreated: now, TimeUpdated: now, Status: db.StatusWaiting,
	}
	payload, err := json.Marshal(map[string]interface{}{"sessionID": info.ID, "session": session})
	if err == nil {
		s.broadcastGlobalEvent("ocman.session.changed", payload)
	}
}
