package server

import (
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/state"
)

// Remote execution observations belong to their owner. Display flags belong to
// the hub where the user viewed, pinned, snoozed or archived that remote row.
func applyRemoteSidebarFlags(rows []db.Session, flags state.SidebarFlags) {
	for i := range rows {
		row := &rows[i]
		key := state.Key{Platform: row.Platform, SessionID: row.ID}
		seen, known := flags.Seen[key]
		if row.SeenTimeUpdated != seen.TimeUpdated {
			row.UnreadCount = 0
		}
		row.SeenTimeUpdated = seen.TimeUpdated
		row.Seen = known && seen.TimeUpdated >= row.TimeUpdated && (row.Status != db.StatusInterrupted || seen.Interrupted)
		if row.Seen {
			row.UnreadCount = 0
		}
		row.PinnedAt = flags.Pinned[key]
		row.Pinned = row.PinnedAt > 0
		row.SnoozedUntil = flags.Snoozed[key]
		at, archived := flags.Archived[key]
		row.Archived = archived && !shouldResurfaceSession(*row, at, flags.Mode)
		if at := flags.Projects[projectArchiveKey(row.RemoteID, row.Directory)]; at > 0 && row.TimeUpdated <= at {
			row.Archived = true
		}
	}
}
