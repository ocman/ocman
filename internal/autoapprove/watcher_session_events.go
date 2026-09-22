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

func (w *autoApproveWatcher) forgetSession(sessionID string) {
	w.seenMu.Lock()
	delete(w.seenSessions, sessionID)
	w.seenMu.Unlock()
}
