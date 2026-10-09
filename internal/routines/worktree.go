package routines

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

func (s *Service) createRunSession(ctx context.Context, host hostsvc.Host, ensured *hostsvc.EnsureProjectOpencodeResult, platformID string, run state.RoutineRun, rulesJSON string, link func(string) error) (string, error) {
	var rules []platforms.PermissionRule
	if err := json.Unmarshal([]byte(rulesJSON), &rules); err != nil || rules == nil {
		rules = []platforms.PermissionRule{}
	}
	title := run.RoutineName + " " + s.now().Format("2006-01-02 15:04")
	if !run.Worktree {
		result, err := s.sessions.CreateRoutine(ctx, platformID, platforms.CreateSessionRequest{Directory: run.Directory, Title: title, Port: ensured.Port()}, rules, run.RoutineID, link)
		if err != nil {
			return "", err
		}
		return result.ID, nil
	}
	done := s.sessions.BeginRoutineCreation()
	defer done()
	created, err := host.CreateWorktreeSession(ctx, hostsvc.WorktreeSessionRequest{ProjectDir: run.Directory, Branch: "routine/" + run.ID, NewBranch: true, MustCreateBranch: true, Title: title, PermissionRules: rules})
	if err != nil {
		return "", err
	}
	if created == nil || created.Reused || created.BranchExisted || created.WorktreePath == "" || created.SessionID == "" {
		return "", fmt.Errorf("routine requires a freshly created worktree session")
	}
	if err := s.store.SetRoutineRunWorktree(ctx, run.ID, created.WorktreePath); err != nil {
		return "", err
	}
	if err := link(created.SessionID); err != nil {
		return "", err
	}
	return created.SessionID, nil
}

// Cleanup removes only the exact workspace created for this occurrence, without
// force. The branch keeps committed work available after the checkout is gone.
func (s *Service) cleanupRunWorktree(ctx context.Context, run state.RoutineRun, detail *platforms.SessionDetail) string {
	if !run.Worktree || !run.CleanupWorktree || run.WorktreePath == "" {
		return ""
	}
	for _, session := range detail.SessionTree {
		if session.Status == db.StatusBusy || session.Status == db.StatusWaiting {
			return "Worktree retained: a child session still needs attention."
		}
	}
	host, ok := s.router.LookupRemote(run.RemoteID)
	platform, available := s.platforms.Get(platforms.ID(run.Platform))
	if !available {
		return "Worktree retained: session platform is unavailable."
	}
	sessions, err := platform.Sessions(ctx, run.WorktreePath, 0)
	if err != nil {
		return "Worktree retained: " + err.Error()
	}
	for _, session := range sessions {
		if session.Status == db.StatusBusy || session.Status == db.StatusWaiting {
			return "Worktree retained: a session still needs attention."
		}
	}
	if !ok {
		return "Worktree cleanup failed: owning machine is unavailable."
	}
	worktrees, err := host.ListWorktrees(ctx, run.Directory)
	if err != nil {
		return "Worktree cleanup failed: " + err.Error()
	}
	for _, worktree := range worktrees {
		if filepath.Clean(worktree.Path) != filepath.Clean(run.WorktreePath) {
			continue
		}
		if worktree.Branch != "routine/"+run.ID || filepath.Clean(run.WorktreePath) == filepath.Clean(run.Directory) {
			return "Worktree retained: workspace identity changed."
		}
		if err := host.RemoveWorktree(ctx, hostsvc.RemoveWorktreeRequest{Dir: run.Directory, Path: run.WorktreePath}); err != nil {
			return "Worktree cleanup failed: " + err.Error()
		}
		return ""
	}
	return "" // Already removed, including recovery after a crash during cleanup.
}
