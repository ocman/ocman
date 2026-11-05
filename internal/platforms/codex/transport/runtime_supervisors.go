package codextransport

import (
	"context"
	"fmt"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms/provider"
)

func (p *Platform) cleanupRuntimeInventory(ctx context.Context) ([]db.Session, error) {
	p.mu.Lock()
	adapter, blocked := p.adapter, p.closed || p.draining
	p.mu.Unlock()
	if blocked {
		return nil, fmt.Errorf("codex supervisor is closed or draining")
	}
	var rows []db.Session
	if adapter != nil {
		var err error
		rows, err = adapter.RuntimeSessions(ctx)
		if err != nil {
			return nil, err
		}
	}
	if p.mgr != nil {
		if p.mgr.State != nil && p.mgr.Runtime == nil {
			return nil, fmt.Errorf("codex supervisor runtime unavailable")
		}
		persisted, err := provider.PersistedRuntimeSessions(ctx, "codex", p.mgr.State, p.mgr.Runtime)
		if err != nil {
			return nil, err
		}
		rows = append(rows, persisted...)
	}
	p.mu.Lock()
	blocked = p.closed || p.draining
	p.mu.Unlock()
	if blocked {
		return nil, fmt.Errorf("codex supervisor is closed or draining")
	}
	return rows, nil
}
