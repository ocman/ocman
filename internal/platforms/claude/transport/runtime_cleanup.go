package claudecode

import (
	"context"
	"fmt"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

var _ platforms.WorktreeRuntimeReader = (*claudePlatform)(nil)

// WorktreeRuntimeSessions is a safety inventory, not the filtered display list.
// It refreshes the physical index, includes native children and joins every
// attached supervisor's pre-transcript inventory without hiding moved sessions.
func (p *claudePlatform) WorktreeRuntimeSessions(ctx context.Context) ([]db.Session, error) {
	if p.history == nil {
		return nil, platforms.ErrHistoryUnavailable
	}
	entries, err := p.history.Inventory(ctx)
	if err != nil {
		return nil, err
	}
	rows := make([]db.Session, 0, len(entries))
	seen := map[string]string{}
	for _, entry := range entries {
		row, err := p.SessionIdentityMetadata(ctx, entry.ID)
		if err != nil {
			return nil, err
		}
		if row == nil || row.ID != entry.ID || row.Directory == "" {
			return nil, fmt.Errorf("incomplete Claude runtime identity")
		}
		rows = append(rows, *row)
		seen[row.ID] = row.Directory
	}
	if p.projects != nil {
		live, err := p.projects.historyRuntimeInventory(ctx)
		if err != nil {
			return nil, err
		}
		for _, row := range live {
			if row.ID == "" || row.Directory == "" {
				return nil, platforms.ErrHistoryUnavailable
			}
			if dir, ok := seen[row.ID]; ok {
				if dir != row.Directory {
					return nil, fmt.Errorf("claude runtime directory disagrees with its source")
				}
				continue
			}
			rows = append(rows, row)
			seen[row.ID] = row.Directory
		}
	}
	return rows, nil
}

// WorktreeRuntimeLifecycle checks fresh scalar metadata and existing live state.
// It never launches or adopts a project runtime to perform a cleanup probe.
func (p *claudePlatform) WorktreeRuntimeLifecycle(ctx context.Context, id string) (db.SessionLifecycle, error) {
	row, err := p.SessionIdentityMetadata(ctx, id)
	if err != nil {
		return db.SessionLifecycle{}, err
	}
	if row == nil || row.Directory == "" {
		return db.SessionLifecycle{}, fmt.Errorf("missing Claude runtime identity")
	}
	out := db.SessionLifecycle{Directory: row.Directory, Status: row.Status, LatestMessageCreated: row.TimeUpdated}
	if p.projects != nil {
		rows, err := p.projects.historyRuntimeInventory(ctx)
		if err != nil {
			return db.SessionLifecycle{}, err
		}
		for _, runtime := range rows {
			if runtime.ID != id {
				continue
			}
			if runtime.Directory != row.Directory {
				return db.SessionLifecycle{}, fmt.Errorf("claude runtime directory disagrees with its source")
			}
			out.Status = runtime.Status
			out.LatestMessageCreated = max(out.LatestMessageCreated, runtime.TimeUpdated)
		}
		if live := p.LiveStatus(id); live != nil {
			if live.Status == "busy" || live.Status == "waiting" {
				out.Status = db.SessionStatus(live.Status)
			}
			if live.PendingPermission || live.PendingQuestion {
				out.Status = db.StatusWaiting
			}
		}
	}
	return out, nil
}
