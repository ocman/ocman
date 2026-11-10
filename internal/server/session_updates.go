package server

import (
	"context"
	"encoding/json"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessionupdates"
)

func (s *Server) sessionUpdatesModule() *sessionupdates.Module {
	return s.sessionUpdatesV.get(func() *sessionupdates.Module {
		return sessionupdates.New(sessionupdates.Deps{
			Read: func(ctx context.Context, key sessionupdates.Key) (*db.Session, error) {
				adapter, ok := s.registry.Get(platforms.ID(key.Platform))
				if !ok {
					return nil, platforms.ErrNotFound
				}
				reader, ok := adapter.(platforms.SidebarSessionReader)
				if !ok {
					return nil, platforms.ErrUnsupported
				}
				row, err := reader.SidebarSession(ctx, key.SessionID)
				if err != nil || row == nil {
					return nil, err
				}
				rows := []db.Session{*row}
				if err := s.overlayPRWatchTasks(ctx, rows); err != nil {
					return nil, err
				}
				if err := s.enrichSidebarCreators(ctx, rows); err != nil {
					return nil, err
				}
				// The adapter keeps its native identity for enrichment and hooks.
				// Only the published row carries the immutable registry key.
				rows[0].Platform = key.Platform
				return &rows[0], nil
			},
			Publish: func(update sessionupdates.Update) {
				payload := map[string]any{"platform": update.Platform, "sessionID": update.SessionID, "revision": update.Revision}
				if update.Row != nil && update.Row.ID == update.SessionID && update.Row.Platform == update.Platform {
					payload["session"], payload["patch"] = update.Row, update.Row
				}
				data, _ := json.Marshal(payload)
				s.broadcastGlobalEvent("ocman.session.changed", data)
			},
		})
	})
}

func (s *Server) enqueueSidebarChange(platform, id string) bool {
	if s.broadcastHub == nil || !s.broadcastHub.hasSubscribers() {
		return false
	}
	if s.registry == nil {
		return false
	}
	var resolved bool
	platform, resolved = s.publicSessionPlatform(platform, id)
	if !resolved {
		return false
	}
	adapter, ok := s.registry.Get(platforms.ID(platform))
	if !ok {
		return false
	}
	if _, ok := adapter.(platforms.SidebarSessionReader); !ok {
		return false
	}
	return s.sessionUpdatesModule().Submit(sessionupdates.Key{Platform: platform, SessionID: id}, s.sessionRevision.Add(1))
}

func (h *broadcastHub) hasSubscribers() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs) > 0
}

// Assign ordering before slow snapshot reads, not when they finally publish.
// Partial events share this sequence so a delayed row cannot rewind live status.
func (s *Server) versionSessionEvent(event string, data []byte) []byte {
	if event != "ocman.session.changed" {
		return data
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return data
	}
	var platform, id string
	_ = json.Unmarshal(fields["platform"], &platform)
	_ = json.Unmarshal(fields["sessionID"], &id)
	if platform == "" || id == "" {
		return data
	}
	if _, ok := fields["revision"]; !ok {
		fields["revision"], _ = json.Marshal(s.sessionRevision.Add(1))
		data, _ = json.Marshal(fields)
	}
	return data
}

func (s *Server) broadcastSidebarChanged(platform, id string) {
	s.sessionSnapshots.invalidate()
	if !s.enqueueSidebarChange(platform, id) {
		s.broadcastSessionPatch(platform, id, nil)
	}
}
