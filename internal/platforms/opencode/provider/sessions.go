package opencode

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/db"
)

// Sessions is the native runtime inventory used by identity migration and
// reload safety. It reads the OpenCode database directly, settles each status
// against the live turn signal and marks pending prompts. Browsing lists come
// from statev2 through the canonical adapter.
func (a *Adapter) Sessions(ctx context.Context, dir string, since int64) ([]db.Session, error) {
	if a.db == nil {
		return nil, nil
	}
	sessions, err := a.db.GetSessions(ctx, dir, since)
	if err != nil {
		return nil, err
	}
	ports := a.rt.DiscoverOpenCodePortsContext(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	permissions, questions := a.prompts.pendingSessionIDs()
	for i := range sessions {
		row := &sessions[i]
		port := a.rt.portForDirectory(ports, row.Directory)
		row.Platform = string(PlatformID)
		row.Status = a.settleStatusOnPort(row.ID, port, row.Status)
		row.LiveConnection = port != ""
		row.PendingPermission, row.PendingQuestion = permissions[row.ID], questions[row.ID]
	}
	return sessions, nil
}
