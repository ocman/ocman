package server

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/prworkflow"
	"github.com/ocman/ocman/internal/state"
)

// overlayPRWatchTasks adds owner-local work after platform observations. It must
// precede completion decisions without changing the agent's foreground status.
func (s *Server) overlayPRWatchTasks(ctx context.Context, sessions []db.Session) error {
	if len(sessions) == 0 {
		return nil
	}
	counts, err := prworkflow.BackgroundCounts(ctx, s.stateDB)
	if err != nil {
		return err
	}
	// Read each owner's watch counts once, not once per session. Failure keeps
	// the last owner observation rather than silently clearing background work.
	type ownerResult struct{ counts map[string]int }
	remoteCounts := map[string]*ownerResult{}
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for _, row := range sessions {
		owner, _ := platforms.SplitPlatformID(row.Platform)
		if owner == "" {
			continue
		}
		if _, read := remoteCounts[row.Platform]; read {
			continue
		}
		result := &ownerResult{}
		remoteCounts[row.Platform] = result
		if p, ok := s.registry.Get(platforms.ID(row.Platform)); ok {
			if workflow, ok := p.(platforms.PullRequestWorkflow); ok {
				wg.Add(1)
				go func() {
					defer wg.Done()
					response, err := workflow.PullRequestOperation(readCtx, platforms.PullRequestRequest{Operation: "counts"})
					var counts map[string]int
					if err == nil && response.Status == 200 && json.Unmarshal(response.Body, &counts) == nil {
						result.counts = counts
					}
				}()
			}
		}
	}
	wg.Wait()
	var watched []int
	for i := range sessions {
		count := counts[state.Key{Platform: sessions[i].Platform, SessionID: sessions[i].ID}]
		if owner, _ := platforms.SplitPlatformID(sessions[i].Platform); owner != "" {
			ownerCount := sessions[i].ApplicationBackgroundTaskCount
			if result := remoteCounts[sessions[i].Platform]; result != nil && result.counts != nil {
				ownerCount = result.counts[sessions[i].ID]
			}
			// During the legacy transfer both databases can briefly contain the
			// same watch. Never double-count it.
			count = max(count, ownerCount)
		}
		sessions[i].BackgroundTaskCount -= sessions[i].ApplicationBackgroundTaskCount
		sessions[i].ApplicationBackgroundTaskCount = count
		if count := sessions[i].ApplicationBackgroundTaskCount; count > 0 {
			sessions[i].BackgroundTaskCount += count
			watched = append(watched, i)
		}
	}
	if len(watched) == 0 || s.stateDB == nil {
		return nil
	}
	keys := make([]state.Key, 0, len(watched))
	for _, i := range watched {
		keys = append(keys, state.Key{Platform: sessions[i].Platform, SessionID: sessions[i].ID})
	}
	archives, mode, err := s.stateDB.SidebarArchives(ctx, keys)
	if err != nil {
		return err
	}
	// Compact owner reads already settled archive visibility before the hub's
	// watches were added. Reapply only the completion hold, preserving other flags.
	for _, i := range watched {
		row := &sessions[i]
		if at, archived := archives[state.Key{Platform: row.Platform, SessionID: row.ID}]; archived && !db.ShouldResurfaceSession(*row, at, mode) {
			row.Archived = true
		}
	}
	return nil
}
