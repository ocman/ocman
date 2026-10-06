package remote

import "github.com/NoUseFreak/ocman/internal/db"

// CachedSession supplies identity for best-effort refresh hints without an RPC
// or transcript read. A session absent from the last listing yields no hint.
func (p *remotePlatform) CachedSession(sessionID string) (db.Session, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, session := range p.lastSess {
		if session.ID == sessionID {
			return session, true
		}
	}
	return db.Session{}, false
}

// stamp annotates live sessions with host identity and records them for
// Owns and the offline fallback. Only an unfiltered listing is complete,
// so only it replaces the fallback snapshot and the ownership set; a
// dir/since-filtered listing just adds the IDs it saw to ownership.
func (p *remotePlatform) stamp(sessions []db.Session, dir string, since int64) []db.Session {
	rid := p.remoteID()
	name := p.nameFn()
	for i := range sessions {
		sessions[i].Platform = string(p.ID())
		sessions[i].RemoteID = rid
		sessions[i].RemoteName = name
		sessions[i].Stale = false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if dir == "" && since == 0 {
		p.owned = make(map[string]struct{}, len(sessions))
		p.lastSess = sessions
	}
	for _, s := range sessions {
		p.owned[s.ID] = struct{}{}
	}
	return sessions
}

// staleSessions returns the last complete listing, filtered to this
// request's dir/since and flagged stale (Phase 5). The filter mirrors the
// owner's (opencode filterSessions): exact directory match, updated at or
// after since.
func (p *remotePlatform) staleSessions(dir string, since int64) []db.Session {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]db.Session, 0, len(p.lastSess))
	for _, s := range p.lastSess {
		if (dir != "" && s.Directory != dir) || (since > 0 && s.TimeUpdated < since) {
			continue
		}
		s.Stale = true
		out = append(out, s)
	}
	return out
}
