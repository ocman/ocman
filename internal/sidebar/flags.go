package sidebar

import "github.com/ocman/ocman/internal/db"

// Flags is the viewer's display state for one session row. Local rows read it
// from the sidebar projection; remote rows read it from the hub's state.
type Flags struct {
	Seen, SeenInterrupted bool
	Archived              bool
	ArchivedAt            int64
	Mode                  string
	ProjectArchivedAt     int64
}

// ApplyFlags derives Seen, Pinned and Archived after the row's observations,
// SeenTimeUpdated and PinnedAt are set. A seen mark covers only the version it
// saw, and an interrupted row also needs its interruption seen. An archived row
// resurfaces on newer activity; a project archive hides rows not updated since.
func ApplyFlags(row *db.Session, f Flags) {
	row.Seen = f.Seen && row.SeenTimeUpdated >= row.TimeUpdated && (row.Status != db.StatusInterrupted || f.SeenInterrupted)
	if row.Seen {
		row.UnreadCount = 0
	}
	row.Pinned = row.PinnedAt > 0
	row.Archived = f.Archived && !db.ShouldResurfaceSession(*row, f.ArchivedAt, f.Mode)
	row.Archived = row.Archived || (f.ProjectArchivedAt > 0 && row.TimeUpdated <= f.ProjectArchivedAt)
}
