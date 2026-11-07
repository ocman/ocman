package server

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/ocman/ocman/internal/db"
	internalmcp "github.com/ocman/ocman/internal/mcp"
	"github.com/ocman/ocman/internal/state"
	log "github.com/sirupsen/logrus"
)

// deferredArchiveGrace stamps an archive applied at a turn's idle edge past
// that edge: agents write once more after going idle, and any write after
// the archive stamp resurfaces the session.
const deferredArchiveGrace = 5 * time.Second

// deferredArchives holds busy local sessions awaiting their idle edge.
type deferredArchives struct {
	mu   sync.Mutex
	keys map[state.Key]bool
}

func (d *deferredArchives) add(platform, sessionID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.keys == nil {
		d.keys = map[state.Key]bool{}
	}
	d.keys[state.Key{Platform: platform, SessionID: sessionID}] = true
}

// take removes a pending archive, reporting whether one was pending.
func (d *deferredArchives) take(platform, sessionID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	key := state.Key{Platform: platform, SessionID: sessionID}
	pending := d.keys[key]
	delete(d.keys, key)
	return pending
}

// SetSessionArchived archives or unarchives a session for the MCP sessions
// tool through the same mutation as POST /api/session/archive. A busy
// session's own activity would resurface an immediate archive, so a busy
// local session is archived on its next idle edge instead. The hub sees no
// idle edge for remote sessions, so a busy remote session is rejected.
func (s sessionMCPService) SetSessionArchived(ctx context.Context, platform, id string, archived bool) (internalmcp.ArchivedSession, error) {
	if s.server.stateDB == nil {
		return internalmcp.ArchivedSession{}, errors.New("state database unavailable")
	}
	platform, id, detail, err := s.resolveTarget(ctx, platform, id)
	if err != nil {
		return internalmcp.ArchivedSession{}, err
	}
	result := internalmcp.ArchivedSession{Platform: platform, SessionID: id, Archived: archived}
	if !archived || detail.Session.Status != db.StatusBusy {
		return result, s.server.setSessionArchived(ctx, platform, id, detail.Session.TimeUpdated, archived)
	}
	if isRemotePlatformID(platform) {
		return internalmcp.ArchivedSession{}, internalmcp.ErrArchiveBusyRemote
	}
	s.server.deferredArchives.add(platform, id)
	// The turn may have ended between the status read and add, so its idle
	// edge has passed: archive now instead of waiting for the next turn.
	if detail, err := s.readTarget(ctx, platform, id); err == nil && detail.Session.Status != db.StatusBusy && s.server.deferredArchives.take(platform, id) {
		return result, s.server.archiveAfterTurn(ctx, platform, id)
	}
	result.Archived, result.Deferred = false, true
	return result, nil
}

// applyDeferredArchive archives a session whose archive waited for this idle edge.
func (s *Server) applyDeferredArchive(platform, sessionID string) {
	if !s.deferredArchives.take(platform, sessionID) {
		return
	}
	ctx, cancel := s.lifetimeCall()
	defer cancel()
	if err := s.archiveAfterTurn(ctx, platform, sessionID); err != nil {
		log.WithError(err).WithField("session", sessionID).Warn("applying deferred session archive")
	}
}

func (s *Server) archiveAfterTurn(ctx context.Context, platform, sessionID string) error {
	return s.setSessionArchived(ctx, platform, sessionID, time.Now().Add(deferredArchiveGrace).UnixMilli(), true)
}
