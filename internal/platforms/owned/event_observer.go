package owned

import (
	"context"
	"maps"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

// The embedded observer forwards port-only fencing and seeding unchanged.
// Session-bearing calls translate before accessing the integration's caches.
type eventObserver struct {
	platforms.OpenCodeEventObserver
	adapter *Adapter
}

func (a *Adapter) OpenCodeObserver() platforms.OpenCodeEventObserver {
	native := platforms.OpenCodeObserver(a.native)
	if native == nil {
		return nil
	}
	return &eventObserver{OpenCodeEventObserver: native, adapter: a}
}

func (o *eventObserver) ObserveSessionStatus(port string, generation uint64, id, status string, notices ...*db.SessionNotice) bool {
	native, err := o.adapter.nativeID(context.Background(), id)
	if err != nil {
		return false
	}
	return o.OpenCodeEventObserver.ObserveSessionStatus(port, generation, native, status, notices...)
}

func (o *eventObserver) SessionStatusOnPort(id, port string) (db.SessionStatus, error) {
	row, err := o.adapter.storedSummary(context.Background(), id)
	if err != nil {
		return "", err
	}
	return row.Status, nil
}

func (o *eventObserver) ObservePromptResolved(directory, kind, id, request string) {
	native, err := o.adapter.nativeID(context.Background(), id)
	if err != nil {
		return
	}
	o.OpenCodeEventObserver.ObservePromptResolved(directory, kind, native, request)
}

func (o *eventObserver) ObservePromptAskedFromPort(port string, generation uint64, directory, kind string, prompt platforms.LivePrompt) {
	translated := maps.Clone(prompt)
	for _, field := range []string{"sessionID", "sessionId", "session_id"} {
		id, ok := prompt[field].(string)
		if !ok || id == "" {
			continue
		}
		native, err := o.adapter.nativeID(context.Background(), id)
		if err != nil {
			return
		}
		translated[field] = native
	}
	o.OpenCodeEventObserver.ObservePromptAskedFromPort(port, generation, directory, kind, translated)
}

func (o *eventObserver) StartPromptReconciliation(ctx context.Context, port string, directories []string, callbacks ...func(platforms.LivePrompt)) <-chan bool {
	return o.OpenCodeEventObserver.StartPromptReconciliation(ctx, port, directories, func(prompt platforms.LivePrompt) {
		rows, err := o.adapter.prompts(ctx, []platforms.LivePrompt{prompt})
		if err != nil {
			return
		}
		for _, callback := range callbacks {
			if callback != nil {
				callback(maps.Clone(rows[0]))
			}
		}
	})
}
