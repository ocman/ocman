package claudecode

import (
	"context"
	"fmt"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms/provider"
)

func (c *claudeProjects) cleanupRuntimeInventory(ctx context.Context) ([]db.Session, error) {
	if err := c.cleanupReady(); err != nil {
		return nil, err
	}
	rows, err := c.historyRuntimeInventory(ctx)
	if err != nil {
		return nil, err
	}
	if c.mgr != nil {
		if c.mgr.State != nil && c.mgr.Runtime == nil {
			return nil, fmt.Errorf("claude supervisor runtime unavailable")
		}
		persisted, err := provider.PersistedRuntimeSessions(ctx, "claudecode", c.mgr.State, c.mgr.Runtime)
		if err != nil {
			return nil, err
		}
		rows = append(rows, persisted...)
	}
	return rows, c.cleanupReady()
}

func (c *claudeProjects) cleanupReady() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || len(c.draining) != 0 {
		return fmt.Errorf("claude supervisor is closed or draining")
	}
	return nil
}
