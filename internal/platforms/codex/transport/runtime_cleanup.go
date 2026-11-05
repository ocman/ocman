package codextransport

import (
	"context"
	"fmt"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
	codexprovider "github.com/ocman/ocman/internal/platforms/codex/provider"
)

var _ platforms.WorktreeRuntimeReader = (*Platform)(nil)

// WorktreeRuntimeSessions includes stored native children and pre-transcript
// sessions in an already running supervisor. It never starts that supervisor.
func (p *Platform) WorktreeRuntimeSessions(ctx context.Context) ([]db.Session, error) {
	if p.history == nil {
		return nil, platforms.ErrHistoryUnavailable
	}
	c, err := p.history.conn(ctx)
	if err != nil {
		return nil, err
	}
	var rows []db.Session
	reader := timedClient{h: p.history, c: c}
	for _, archived := range []bool{false, true} {
		threads, err := reader.ListThreads(ctx, codexappserver.ThreadListParams{SourceKinds: codexappserver.AllSourceKinds, Archived: &archived})
		if err != nil {
			return nil, err
		}
		for i := range threads {
			row := codexprovider.Session(&threads[i])
			if row.ID == "" || row.Directory == "" {
				return nil, platforms.ErrHistoryUnavailable
			}
			rows = append(rows, row)
		}
	}
	live, err := p.cleanupRuntimeInventory(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]string{}
	for _, row := range rows {
		seen[row.ID] = row.Directory
	}
	for _, row := range live {
		if row.ID == "" || row.Directory == "" {
			return nil, platforms.ErrHistoryUnavailable
		}
		if dir, ok := seen[row.ID]; ok {
			if dir != row.Directory {
				return nil, fmt.Errorf("codex runtime directory disagrees with its source")
			}
			continue
		}
		rows = append(rows, row)
		seen[row.ID] = row.Directory
	}
	return rows, nil
}

func (p *Platform) WorktreeRuntimeLifecycle(ctx context.Context, id string) (db.SessionLifecycle, error) {
	if p.history == nil {
		return db.SessionLifecycle{}, platforms.ErrHistoryUnavailable
	}
	c, err := p.history.conn(ctx)
	if err != nil {
		return db.SessionLifecycle{}, err
	}
	thread, err := (timedClient{h: p.history, c: c}).ReadThread(ctx, id, false)
	if err != nil {
		return db.SessionLifecycle{}, err
	}
	if thread == nil || thread.ID != id || thread.Cwd == "" {
		return db.SessionLifecycle{}, platforms.ErrHistoryUnavailable
	}
	row := codexprovider.Session(thread)
	out := db.SessionLifecycle{Directory: row.Directory, LatestMessageCreated: row.TimeUpdated}
	switch thread.Status.Type {
	case "active":
		out.Status = db.StatusBusy
	case "systemError":
		out.Status = db.StatusError
	case "idle", "notLoaded":
		out.Status = db.StatusDone
	default:
		return db.SessionLifecycle{}, fmt.Errorf("unknown Codex runtime status %q", thread.Status.Type)
	}
	runtime, err := p.cleanupRuntimeInventory(ctx)
	if err != nil {
		return db.SessionLifecycle{}, err
	}
	for _, session := range runtime {
		if session.ID != id {
			continue
		}
		if session.Directory != out.Directory {
			return db.SessionLifecycle{}, fmt.Errorf("codex runtime directory disagrees with its source")
		}
		if session.Status == db.StatusBusy || session.Status == db.StatusWaiting {
			out.Status = session.Status
		}
		if session.PendingPermission || session.PendingQuestion {
			out.Status = db.StatusWaiting
		}
		if session.BackgroundTaskCount != 0 || session.BackgroundServiceCount != 0 {
			out.Status = db.StatusBusy
		}
		out.LatestMessageCreated = max(out.LatestMessageCreated, session.TimeUpdated)
	}
	if adapter := p.current(); adapter != nil {
		life, err := adapter.SessionLifecycle(ctx, id)
		if err != nil {
			return db.SessionLifecycle{}, err
		}
		if life == nil || life.ReadOnly {
			return db.SessionLifecycle{}, platforms.ErrHistoryUnavailable
		}
		if out.Status != db.StatusBusy && out.Status != db.StatusWaiting {
			out.Status = life.Status
		}
		out.LatestMessageID, out.LatestMessageRole = life.LatestMessageID, life.LatestMessageRole
		out.LatestMessageCreated = max(out.LatestMessageCreated, life.LatestMessageCreated)
	}
	return out, nil
}
