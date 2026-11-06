package worktreecleanup

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/git"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
	log "github.com/sirupsen/logrus"
)

type session struct {
	db.Session
	key state.Key
}

func within(root, path string) bool {
	return path != "" && root != "" && (filepath.Clean(path) == filepath.Clean(root) || strings.HasPrefix(filepath.Clean(path), filepath.Clean(root)+string(filepath.Separator)))
}

// inventory fails an entire owner closed if any of its adapters cannot provide
// a complete inventory. A disconnected remote never falls back to local paths.
func (m *Module) inventory(ctx context.Context) (map[string][]session, map[string]bool) {
	owners, failed := map[string][]session{}, map[string]bool{}
	for _, p := range m.d.Platforms() {
		id := string(p.ID())
		remote, _ := platforms.SplitPlatformID(id)
		owner := state.NormalizeRemoteID(remote)
		if m.d.Ready != nil && !m.d.Ready(id) {
			failed[owner] = true
			continue
		}
		readCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		var rows []db.Session
		var err error
		if reader, ok := p.(platforms.WorktreeSessionReader); ok {
			rows, err = reader.WorktreeSessions(readCtx)
		} else {
			rows, err = p.Sessions(readCtx, "", 0)
		}
		cancel()
		if err != nil {
			failed[owner] = true
			continue
		}
		for _, row := range rows {
			owners[owner] = append(owners[owner], session{Session: row, key: state.Key{Platform: id, SessionID: row.ID}})
		}
	}
	return owners, failed
}

func (m *Module) archives(ctx context.Context) (map[state.Key]state.ArchivedSession, error) {
	rows, err := m.d.Store.ArchivedSessionRecords(ctx)
	if err != nil {
		return nil, err
	}
	out := map[state.Key]state.ArchivedSession{}
	for _, row := range rows {
		out[row.Key] = row
	}
	return out, nil
}

func archiveFor(s session, rows []session, archives map[state.Key]state.ArchivedSession) (state.ArchivedSession, bool) {
	visited := map[state.Key]bool{}
	for !visited[s.key] {
		visited[s.key] = true
		if a, ok := archives[s.key]; ok {
			return a, a.SnoozedUntil == 0
		}
		// Only native descendants inherit archive eligibility, never an
		// unrelated application worktree link or a parent in another checkout.
		if s.ParentID == "" || !s.SourceChild {
			break
		}
		found := false
		for _, parent := range rows {
			if parent.key.Platform == s.key.Platform && parent.ID == s.ParentID && parent.Directory == s.Directory {
				s = parent
				found = true
				break
			}
		}
		if !found {
			break
		}
	}
	return state.ArchivedSession{}, false
}

// eligible requires every session in the checkout or a nested directory to
// be archived and quiet. A single unarchived sibling retains the whole tree.
func (m *Module) eligible(ctx context.Context, path string, rows []session) ([]hostsvc.WorktreeSessionCheck, error) {
	archives, err := m.archives(ctx)
	if err != nil {
		return nil, err
	}
	var checks []hostsvc.WorktreeSessionCheck
	for _, s := range rows {
		if !within(path, s.Directory) {
			continue
		}
		a, ok := archiveFor(s, rows, archives)
		if !ok {
			return nil, fmt.Errorf("session %s is not archived", s.ID)
		}
		at, err := m.observe(ctx, s.key, activityStamp(s.Session), a.TimeUpdated)
		if err != nil {
			return nil, err
		}
		if max(a.ArchivedAt, at)+retention.Milliseconds() > m.now().UnixMilli() {
			return nil, fmt.Errorf("session %s is within its seven-day grace period", s.ID)
		}
		queued, err := m.d.Store.CountQueuedMessages(ctx, s.key.Platform, s.ID)
		if err != nil {
			return nil, err
		}
		if queued != 0 || !settled(s.Session) {
			return nil, fmt.Errorf("session %s still has active or pending work", s.ID)
		}
		_, base := platforms.SplitPlatformID(s.key.Platform)
		checks = append(checks, hostsvc.WorktreeSessionCheck{Platform: base, SessionID: s.ID, TimeUpdated: activityStamp(s.Session)})
	}
	if len(checks) == 0 {
		return nil, fmt.Errorf("no owning sessions")
	}
	return checks, nil
}

func settled(s db.Session) bool {
	return (s.Status == db.StatusDone || s.Status == db.StatusError || s.Status == db.StatusInterrupted) &&
		!s.PendingPermission && !s.PendingQuestion && s.BackgroundTaskCount == 0 && s.BackgroundServiceCount == 0 && !s.Stale
}

func activityStamp(s db.Session) int64 {
	return max(s.TimeUpdated, s.LastMessageAt, s.LastUserPromptAt, s.LastTurnCompletedAt, s.LastHaltAt)
}

func (m *Module) Sweep(ctx context.Context) error {
	if m.d.Store == nil {
		return nil
	}
	_ = module.ReportProgress(ctx, module.Progress{Phase: "Reading session inventories"})
	owners, failed := m.inventory(ctx)
	targets, unavailable := m.sweepTargets(owners, failed)
	total := int64(len(targets))
	progress := module.Progress{Phase: "Checking checkout safety", Total: &total, Unit: "checkouts", Counters: map[string]int64{"removed": 0, "retained": 0, "already-removed": 0, "unavailable-owners": unavailable}}
	_ = module.ReportProgress(ctx, progress)
	for _, target := range targets {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		removed, err := m.clean(ctx, target.host, target.maintenance, target.owner, target.path, target.rows)
		switch {
		case removed:
			progress.Counters["removed"]++
		case err != nil:
			progress.Counters["retained"]++
		default:
			progress.Counters["already-removed"]++
		}
		if err != nil {
			log.WithFields(log.Fields{"owner": target.owner, "path": target.path, "reason": err.Error()}).Debug("worktree retained")
		}
		progress.Completed++
		_ = module.ReportProgress(ctx, progress)
	}
	return nil
}

type sweepTarget struct {
	owner, path string
	rows        []session
	host        hostsvc.Host
	maintenance hostsvc.WorktreeMaintenance
}

func (m *Module) sweepTargets(owners map[string][]session, failed map[string]bool) ([]sweepTarget, int64) {
	var targets []sweepTarget
	var unavailable int64
	for _, incomplete := range failed {
		if incomplete {
			unavailable++
		}
	}
	for owner, rows := range owners {
		if failed[owner] {
			continue
		}
		host, ok := m.d.Router().LookupRemote(owner)
		if !ok {
			unavailable++
			continue
		}
		maintenance, ok := host.(hostsvc.WorktreeMaintenance)
		if !ok {
			unavailable++
			continue
		}
		paths := map[string]bool{}
		for _, s := range rows {
			// The owning host performs the authoritative filesystem validation.
			if path := managedCheckout(s.Directory); path != "" {
				paths[path] = true
			}
		}
		for path := range paths {
			targets = append(targets, sweepTarget{owner: owner, path: path, rows: rows, host: host, maintenance: maintenance})
		}
	}
	return targets, unavailable
}

func managedCheckout(dir string) string {
	project := state.ProjectRootForDirectory(dir)
	if project == dir {
		return ""
	}
	parent := filepath.Dir(git.WorktreePathFor(project, "placeholder"))
	rel, err := filepath.Rel(parent, dir)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.Join(parent, strings.Split(rel, string(filepath.Separator))[0])
}

func (m *Module) clean(ctx context.Context, host hostsvc.Host, maintenance hostsvc.WorktreeMaintenance, owner, path string, rows []session) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	ctx, release := m.d.Store.WorktreeMaintenance(ctx)
	defer release()
	checks, err := m.eligible(ctx, path, rows)
	if err != nil {
		return false, err
	}
	project := state.ProjectRootForDirectory(path)
	entries, err := host.ListWorktrees(ctx, project)
	if err != nil {
		return false, err
	}
	var target *git.Worktree
	for i := range entries {
		if filepath.Clean(entries[i].Path) == filepath.Clean(path) {
			target = &entries[i]
			break
		}
	}
	if target == nil {
		return false, nil
	} // removed already; retain recovery receipt
	if target.Main || target.Bare || target.Locked || target.Branch == "" || target.Head == "" {
		return false, fmt.Errorf("unsafe worktree identity")
	}
	checkpoint := hostsvc.WorktreeCheckpoint{Project: project, Path: path, Branch: target.Branch, Head: target.Head}
	if err := m.remember(ctx, owner, checkpoint); err != nil {
		return false, err
	}
	err = maintenance.CleanupWorktree(ctx, hostsvc.CleanupWorktreeRequest{WorktreeCheckpoint: checkpoint, Sessions: checks})
	reason := ""
	if err != nil {
		reason = err.Error()
	}
	// A durable pre-removal receipt is already present. Keep it even if
	// acknowledgement fails or cancellation arrives just after Git succeeds.
	if saveErr := m.outcome(ctx, owner, path, reason, err == nil); saveErr != nil {
		return err == nil, saveErr
	}
	if errors.Is(err, platforms.ErrUnsupported) || errors.Is(err, platforms.ErrRemoteOffline) {
		// These failures prove the owner never admitted removal. Do not
		// make an older owner require an equally unsupported restore RPC.
		if forgetErr := m.forget(ctx, owner, path); forgetErr != nil {
			return false, forgetErr
		}
	}
	return err == nil, err
}
