package opencode

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func (a *Adapter) StoredReadConfig(directory string) *platforms.ConversationHead {
	head := &platforms.ConversationHead{Pricing: a.pricing, DirectoryDefaults: true}
	if directory != "" {
		head.Warnings = a.rt.sessionWarningsForDirectory(directory)
	}
	return head
}

// EnrichStoredSessions reads only managed-port and SSE observation caches, not
// OpenCode's session/message tables. Parent evidence comes from statev2.
func (a *Adapter) EnrichStoredSessions(ctx context.Context, rows []db.Session, parents map[string]string) error {
	ports := a.rt.DiscoverOpenCodePortsContext(ctx)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	permissions, questions := a.prompts.pendingSessionIDs()
	permissions = platforms.BubblePromptSessions(permissions, parents)
	questions = platforms.BubblePromptSessions(questions, parents)
	for i := range rows {
		row := &rows[i]
		port := a.rt.portForDirectory(ports, row.Directory)
		row.Status = a.settleStatusOnPort(row.ID, port, row.Status)
		row.Notice = a.sessionNoticeOnPort(row.ID, port)
		row.LiveConnection = port != ""
		row.PendingPermission, row.PendingQuestion = permissions[row.ID], questions[row.ID]
	}
	return a.enrichStoredHalts(ctx, rows, parents)
}
