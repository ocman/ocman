package server

import (
	"encoding/json"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessionsvc"
)

func (s *Server) sessionEventSink() platforms.SessionEventSink {
	return platforms.SessionEventSink{OnSessionBackgroundChanged: s.broadcastSessionChanged, OnSessionStatus: s.onSessionStatus, OnSessionIdle: func(platform, session string) {
		s.onAgentWaitIdle(platform, session)
		s.onSessionIdle(platform, session)
	}}
}

func (s *Server) onSessionStatus(platformID, sessionID string, status db.SessionStatus, resetsAt int64) {
	if platformID == "" || sessionID == "" {
		return
	}
	if status != db.StatusLimited {
		resetsAt = 0
	}
	payload, err := json.Marshal(map[string]any{"platform": platformID, "sessionID": sessionID,
		"patch": map[string]any{"status": status, "resetsAt": resetsAt}})
	if err == nil {
		s.broadcastGlobalEvent("ocman.session.changed", payload)
	}
}

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
func (s *Server) broadcastSessionChanged(platform, sessionID string) {
	if sessionID == "" {
		return
	}
	payload, err := json.Marshal(map[string]interface{}{"sessionID": sessionID, "platform": platform})
	if err != nil {
		return
	}
	s.broadcastGlobalEvent("ocman.session.changed", payload)
	// Recovery for terminal changes without a matching idle edge.
	// Runs on the caller's goroutine (joined event watchers), bounded by the
	// server lifetime so shutdown cancels its reads.
	s.replyToConversation(s.lifetime(), platform, sessionID)
	s.replyToSession(s.lifetime(), platform, sessionID)
}

func (s *Server) broadcastSessionTitle(platform, sessionID, title string) {
	s.broadcastSessionPatch(platform, sessionID, map[string]interface{}{"title": title})
}

func (s *Server) broadcastSessionPatch(platform, sessionID string, patch map[string]interface{}) {
	if sessionID == "" {
		return
	}
	payload, err := json.Marshal(map[string]interface{}{"sessionID": sessionID, "platform": platform, "patch": patch})
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
		RoutineID: info.RoutineID, Archived: info.Archived, Origin: info.Origin, OriginID: info.OriginID,
	}
	payload, err := json.Marshal(map[string]interface{}{"sessionID": info.ID, "session": session})
	if err == nil {
		s.broadcastGlobalEvent("ocman.session.changed", payload)
	}
}
