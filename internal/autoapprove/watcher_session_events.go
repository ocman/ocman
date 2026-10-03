package autoapprove

import (
	"context"
	"encoding/json"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms/opencode"
	log "github.com/sirupsen/logrus"
)

// markSessionDirtyIfKnown records only events with an identified session.
func (w *autoApproveWatcher) markSessionDirtyIfKnown(sessionID string) {
	if sessionID == "" || w.markSessionDirty == nil {
		return
	}
	w.markSessionDirty(sessionID)
}

// handleSessionDataChanged invalidates message/part aggregates or a deleted row.
// Unattributable events require a full reconciliation.
func (w *autoApproveWatcher) handleSessionDataChanged(sessionID string) {
	if sessionID == "" {
		if w.markSessionsDirty != nil {
			w.markSessionsDirty()
		}
		return
	}
	w.markSessionDirtyIfKnown(sessionID)
	if w.svc != nil {
		payload, _ := json.Marshal(map[string]any{
			"sessionID":   sessionID,
			"timeUpdated": time.Now().UnixMilli(),
		})
		w.svc.broadcastGlobalEvent("ocman.session.activity", payload)
	}
}

// handleSessionChanged refreshes new sessions before announcing them. Known
// sessions are marked dirty without fetching the list on every token.
func (w *autoApproveWatcher) handleSessionChanged(ctx context.Context, sessionID string) {
	if sessionID == "" {
		return
	}
	w.markSessionDirtyIfKnown(sessionID)
	w.seenMu.Lock()
	if _, ok := w.seenSessions[sessionID]; ok {
		w.seenMu.Unlock()
		return
	}
	w.seenSessions[sessionID] = struct{}{}
	w.seenMu.Unlock()
	if w.svc != nil && w.svc.deps.RefreshSession != nil {
		go func() {
			err := w.svc.deps.RefreshSession(ctx, sessionID)
			if ctx.Err() != nil {
				w.forgetSession(sessionID)
				return
			}
			if err != nil {
				log.WithError(err).WithField("session_id", sessionID).Warn("failed to refresh new session")
				w.forgetSession(sessionID)
				opencode.InvalidateSessionsCache()
			}
			if w.svc.deps.BroadcastSessionChanged != nil {
				w.svc.deps.BroadcastSessionChanged(sessionID)
			}
		}()
		return
	}
	opencode.InvalidateSessionsCache()
	if w.svc != nil && w.svc.deps.BroadcastSessionChanged != nil {
		w.svc.deps.BroadcastSessionChanged(sessionID)
	}
}

// handleSessionTitle pushes a rename to the UI. The first title seen for a
// session is pushed too: the watcher may start after the session was
// opened, so that first event can itself be a rename. The snapshot is
// refreshed first so a refetch triggered by the broadcast cannot read the
// old title back.
func (w *autoApproveWatcher) handleSessionTitle(ctx context.Context, sessionID, title string) {
	w.seenMu.Lock()
	prev, known := w.titles[sessionID]
	w.titles[sessionID] = title
	w.seenMu.Unlock()
	if (known && prev == title) || w.svc == nil || w.svc.deps.BroadcastSessionTitle == nil {
		return
	}
	go func() {
		if refresh := w.svc.deps.RefreshSession; refresh != nil {
			if err := refresh(ctx, sessionID); err != nil && ctx.Err() == nil {
				log.WithError(err).WithField("session_id", sessionID).Warn("failed to refresh renamed session")
			}
		}
		if ctx.Err() != nil {
			// Undelivered: let the same title through after reconnect,
			// unless a newer title has claimed the entry meanwhile.
			w.seenMu.Lock()
			if w.titles[sessionID] == title {
				delete(w.titles, sessionID)
			}
			w.seenMu.Unlock()
			return
		}
		// Send the latest title, not the captured one, and publish under
		// the lock: two quick renames may finish their refreshes out of
		// order, and an older read published after a newer one would
		// regress the UI. The broadcast never blocks.
		w.seenMu.Lock()
		defer w.seenMu.Unlock()
		if latest := w.titles[sessionID]; latest != "" {
			w.svc.deps.BroadcastSessionTitle(sessionID, latest)
		}
	}()
}

func (w *autoApproveWatcher) forgetSession(sessionID string) {
	w.seenMu.Lock()
	delete(w.seenSessions, sessionID)
	w.seenMu.Unlock()
}
