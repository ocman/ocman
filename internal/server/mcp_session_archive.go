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

// deferredArchives holds busy local sessions awaiting their idle edge. Its
// lock covers both the pending set and every archive-state write, so
// claiming or cancelling a pending archive and persisting the result are one
// step: a later unarchive cannot be overwritten by an earlier claim.
// ponytail: one lock for all sessions; archive writes are rare and short.
type deferredArchives struct {
	mu   sync.Mutex
	keys map[state.Key]bool
	// claimed is a test seam run after a deferred archive is claimed and
	// before it is written.
	claimed func()
}

func (d *deferredArchives) lock() func() {
	d.mu.Lock()
	return d.mu.Unlock
}

func (d *deferredArchives) add(platform, sessionID string) {
	defer d.lock()()
	if d.keys == nil {
		d.keys = map[state.Key]bool{}
	}
	d.keys[state.Key{Platform: platform, SessionID: sessionID}] = true
}

// cancelLocked removes a pending archive, reporting whether one was pending.
func (d *deferredArchives) cancelLocked(platform, sessionID string) bool {
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
	s.server.deferredArchives.add(s.server.sessionStatePlatform(platform), id)
	// The turn may have ended between the status read and add, so its idle
	// edge has passed: archive now instead of waiting for the next turn.
	if detail, err := s.readTarget(ctx, platform, id); err == nil && detail.Session.Status != db.StatusBusy {
		if applied, err := s.server.applyPendingArchive(ctx, platform, id); applied {
			return result, err
		}
	}
	result.Archived, result.Deferred = false, true
	return result, nil
}

// applyDeferredArchive archives a session whose archive waited for this idle edge.
func (s *Server) applyDeferredArchive(platform, sessionID string) {
	ctx, cancel := s.lifetimeCall()
	defer cancel()
	if _, err := s.applyPendingArchive(ctx, platform, sessionID); err != nil {
		log.WithError(err).WithField("session", sessionID).Warn("applying deferred session archive")
	}
}

// applyPendingArchive claims and writes a pending archive under one lock,
// stamped past the idle edge. It reports whether an archive was pending.
func (s *Server) applyPendingArchive(ctx context.Context, platform, sessionID string) (bool, error) {
	platform = s.sessionStatePlatform(platform)
	unlock := s.deferredArchives.lock()
	defer unlock()
	if !s.deferredArchives.cancelLocked(platform, sessionID) {
		return false, nil
	}
	if s.deferredArchives.claimed != nil {
		s.deferredArchives.claimed()
	}
	return true, s.writeSessionArchived(ctx, platform, sessionID, time.Now().Add(deferredArchiveGrace).UnixMilli(), true)
}
