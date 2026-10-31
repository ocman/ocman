package server

import (
	"context"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/prworkflow"
	"github.com/ocman/ocman/internal/state"
)

// overlayPRWatchTasks adds hub-owned work after platform observations. It must
// precede completion decisions without changing the agent's foreground status.
func (s *Server) overlayPRWatchTasks(ctx context.Context, sessions []db.Session) error {
	if len(sessions) == 0 {
		return nil
	}
	counts, err := prworkflow.BackgroundCounts(ctx, s.stateDB)
	if err != nil {
		return err
	}
	var watched []int
	for i := range sessions {
		sessions[i].BackgroundTaskCount -= sessions[i].ApplicationBackgroundTaskCount
		sessions[i].ApplicationBackgroundTaskCount = counts[state.Key{Platform: sessions[i].Platform, SessionID: sessions[i].ID}]
		if count := sessions[i].ApplicationBackgroundTaskCount; count > 0 {
			sessions[i].BackgroundTaskCount += count
			watched = append(watched, i)
		}
	}
	if len(watched) == 0 {
		return nil
	}
	flags, err := s.stateDB.SidebarFlags(ctx)
	if err != nil {
		return err
	}
	// Compact owner reads already settled archive visibility before the hub's
	// watches were added. Reapply only the completion hold, preserving other flags.
	for _, i := range watched {
		row := &sessions[i]
		if at, archived := flags.Archived[state.Key{Platform: row.Platform, SessionID: row.ID}]; archived && !db.ShouldResurfaceSession(*row, at, flags.Mode) {
			row.Archived = true
		}
	}
	return nil
}
