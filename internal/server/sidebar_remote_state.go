package server

import (
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/projectsmod"
	"github.com/ocman/ocman/internal/sidebar"
	"github.com/ocman/ocman/internal/state"
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
		row.PinnedAt = flags.Pinned[key]
		row.SnoozedUntil = flags.Snoozed[key]
		at, archived := flags.Archived[key]
		sidebar.ApplyFlags(row, sidebar.Flags{Seen: known, SeenInterrupted: seen.Interrupted, Archived: archived, ArchivedAt: at, Mode: flags.Mode,
			ProjectArchivedAt: flags.Projects[projectsmod.ArchiveKey(row.RemoteID, row.Directory)]})
	}
}
