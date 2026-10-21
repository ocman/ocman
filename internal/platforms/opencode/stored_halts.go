package opencode

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func (a *Adapter) SetSessionHaltIdentity(toPublic, toNative func(context.Context, string) (string, error)) {
	a.prompts.mu.Lock()
	defer a.prompts.mu.Unlock()
	a.prompts.haltToPublic, a.prompts.haltToNative = toPublic, toNative
}

func (a *Adapter) enrichStoredHalts(ctx context.Context, rows []db.Session, parents map[string]string) error {
	a.prompts.mu.RLock()
	halts := make(map[string]int64, len(a.prompts.lastHalts))
	for id, at := range a.prompts.lastHalts {
		halts[id] = at
	}
	store, toNative := a.prompts.haltStore, a.prompts.haltToNative
	a.prompts.mu.RUnlock()
	if store != nil {
		stored, err := store.SessionHalts(ctx)
		if err != nil {
			return err
		}
		for id, at := range stored {
			if toNative != nil {
				id, err = toNative(ctx, id)
				if err != nil {
					return err
				}
			}
			if id != "" {
				halts[id] = max(halts[id], at)
			}
		}
	}
	bubbled := map[string]int64{}
	for id, at := range halts {
		for root := range platforms.BubblePromptSessions(map[string]bool{id: true}, parents) {
			bubbled[root] = max(bubbled[root], at)
		}
	}
	for i := range rows {
		rows[i].LastHaltAt = max(halts[rows[i].ID], bubbled[rows[i].ID])
	}
	return nil
}
