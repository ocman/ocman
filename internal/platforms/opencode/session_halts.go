package opencode

import (
	"context"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
	log "github.com/sirupsen/logrus"
)

// Caller holds the prompt registry lock. Replayed snapshots keep the request's
// original receipt. Test adapters without state retain receipts in memory.
func (r *livePromptRegistry) recordHalt(kind string, prompt platforms.LivePrompt) {
	sessionID, requestID := prompt.StringField("sessionID"), prompt.StringField("id")
	if sessionID == "" || requestID == "" {
		return
	}
	key := promptKey(kind, sessionID, requestID)
	if r.haltReceipts == nil {
		r.haltReceipts = make(map[string]int64)
	}
	if _, exists := r.haltReceipts[key]; exists {
		return
	}
	at := time.Now().UnixMilli()
	if r.haltStore != nil {
		var err error
		storedID := sessionID
		if r.haltToPublic != nil {
			storedID, err = r.haltToPublic(context.Background(), sessionID)
		}
		if err == nil {
			at, err = r.haltStore.RecordSessionHalt(context.Background(), storedID, kind, requestID, at)
		}
		if err != nil {
			log.WithError(err).Warn("recording session halt")
			return
		}
	}
	r.haltReceipts[key] = at
	if r.lastHalts == nil {
		r.lastHalts = make(map[string]int64)
	}
	r.lastHalts[sessionID] = max(r.lastHalts[sessionID], at)
}
