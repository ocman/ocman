package codextransport

import (
	"context"
	"fmt"
	"strings"

	"github.com/ocman/ocman/internal/ocruntime"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/provider"
)

// ReloadIdle reads live supervisor ownership, not thread history. Accepted
// sends outlive their request leases, and prompts can remain after a turn ends.
// Unreadable attached or persisted supervisors must keep detachment fenced.
func (p *Platform) ReloadIdle(ctx context.Context) (bool, error) {
	p.ensureMu.Lock()
	defer p.ensureMu.Unlock()
	if p.mgr == nil {
		return false, fmt.Errorf("codex supervisor ownership is unavailable")
	}
	rows, err := p.cleanupRuntimeInventory(ctx)
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		if platforms.ReloadSessionActive(row) {
			return false, nil
		}
	}
	// Runtime inventory provides turn state, but pending prompt flags are not
	// part of every surviving supervisor's rows. Read prompt ownership directly,
	// including persisted supervisors not yet attached by background recovery.
	p.mu.Lock()
	attached, endpoint := p.adapter != nil, p.endpoint
	p.mu.Unlock()
	if attached {
		// A temporary reader has no approval rules. The attached adapter may
		// hide prompts currently being auto-answered, which still block detach.
		a, err := provider.New(ctx, strings.TrimPrefix(endpoint, "unix://"))
		if err != nil {
			return false, err
		}
		idle, err := reloadSupervisorIdle(ctx, a)
		a.Close()
		if err != nil || !idle {
			return false, err
		}
	}
	if p.mgr.State != nil {
		roots, err := p.mgr.State.ListManagedProviders(ctx, "codex")
		if err != nil {
			return false, err
		}
		for _, root := range roots {
			row, exists, err := p.mgr.State.GetManagedProvider(ctx, "codex", root)
			if err != nil {
				return false, err
			}
			if !exists {
				continue
			}
			a, err := provider.New(ctx, strings.TrimPrefix(row.Endpoint, "unix://"))
			if err != nil {
				alive, aliveErr := p.mgr.Runtime.Alive(ctx, &ocruntime.Instance{Endpoint: row.Endpoint, Kind: row.Kind, ID: row.RuntimeID, PID: row.PID, RepoRoot: root})
				if aliveErr != nil || alive {
					return false, fmt.Errorf("cannot inspect persisted Codex prompts: %w", err)
				}
				continue
			}
			idle, err := reloadSupervisorIdle(ctx, a)
			a.Close()
			if err != nil || !idle {
				return false, err
			}
		}
	}
	return true, ctx.Err()
}

func reloadSupervisorIdle(ctx context.Context, a *provider.Adapter) (bool, error) {
	if a.ID() != "codex" || !a.Capabilities().RespondPermission || !a.Capabilities().RespondQuestion {
		return false, fmt.Errorf("codex supervisor cannot verify pending prompts")
	}
	rows, err := a.RuntimeSessions(ctx)
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		if platforms.ReloadSessionActive(row) {
			return false, nil
		}
		permissions, err := a.RefreshPermissions(ctx, row.ID)
		if err != nil {
			return false, err
		}
		questions, err := a.RefreshQuestions(ctx, row.ID)
		if err != nil {
			return false, err
		}
		if len(permissions) != 0 || len(questions) != 0 {
			return false, nil
		}
	}
	return true, nil
}

var _ platforms.ReloadIdleReader = (*Platform)(nil)
