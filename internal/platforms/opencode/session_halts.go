package opencode

import (
	"context"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	log "github.com/sirupsen/logrus"
)

// Caller holds the prompt registry lock. Replayed snapshots keep the request's
// original receipt. Test adapters without state retain receipts in memory.
func (r *livePromptRegistry) recordHalt(kind string, prompt platforms.LivePrompt) {
	sessionID, requestID := promptString(prompt, "sessionID"), promptString(prompt, "id")
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
		at, err = r.haltStore.RecordSessionHalt(context.Background(), sessionID, kind, requestID, at)
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

func (a *Adapter) attachSessionHalts(ctx context.Context, sessions []db.Session) error {
	a.prompts.mu.RLock()
	halts := make(map[string]int64, len(a.prompts.lastHalts))
	for id, at := range a.prompts.lastHalts {
		halts[id] = at
	}
	store := a.prompts.haltStore
	a.prompts.mu.RUnlock()
	if store != nil {
		stored, err := store.SessionHalts(ctx)
		if err != nil {
			return err
		}
		for id, at := range stored {
			halts[id] = max(halts[id], at)
		}
	}
	ids := make([]string, 0, len(halts))
	for id := range halts {
		ids = append(ids, id)
	}
	parents, err := a.db.GetSessionParentIDs(ctx, ids)
	if err != nil {
		return err
	}
	for child, parent := range parents {
		halts[parent] = max(halts[parent], halts[child])
	}
	for i := range sessions {
		sessions[i].LastHaltAt = halts[sessions[i].ID]
	}
	return nil
}
