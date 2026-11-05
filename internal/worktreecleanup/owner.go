package worktreecleanup

import (
	"context"
	"errors"
	"fmt"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
)

// AdmitCleanup repeats the inventory and activity checks on the filesystem
// owner, holding its mutation fence through the host's final Git checks/remove.
func (m *Module) AdmitCleanup(ctx context.Context, req hostsvc.CleanupWorktreeRequest) (context.Context, func(), error) {
	if m.d.Store == nil {
		return ctx, nil, fmt.Errorf("cleanup requires durable state")
	}
	ctx, release := m.d.Store.WorktreeMaintenance(ctx)
	if err := m.verifyOwner(ctx, req); err != nil {
		release()
		return ctx, nil, err
	}
	if err := m.remember(ctx, state.LocalRemoteID, req.WorktreeCheckpoint); err != nil {
		release()
		return ctx, nil, err
	}
	return ctx, release, nil
}

func (m *Module) verifyOwner(ctx context.Context, req hostsvc.CleanupWorktreeRequest) error {
	if m.d.Idle != nil {
		if err := m.d.Idle(ctx); err != nil {
			return err
		}
	}
	if len(req.Sessions) == 0 {
		return fmt.Errorf("missing session safety snapshot")
	}
	expected := map[state.Key]int64{}
	for _, s := range req.Sessions {
		expected[state.Key{Platform: s.Platform, SessionID: s.SessionID}] = s.TimeUpdated
	}
	if err := m.verifyRuntimeInventory(ctx, req.Path, expected); err != nil {
		return err
	}
	owners, failed := m.inventory(ctx)
	if failed[state.LocalRemoteID] {
		return fmt.Errorf("owner inventory is incomplete")
	}
	seen := 0
	for _, s := range owners[state.LocalRemoteID] {
		if !within(req.Path, s.Directory) {
			continue
		}
		updated, ok := expected[s.key]
		if !ok || updated != activityStamp(s.Session) || !settled(s.Session) {
			return fmt.Errorf("session inventory changed or work is active")
		}
		seen++
		var at int64
		if err := m.d.Store.SQL().QueryRowContext(ctx, `SELECT COALESCE(MAX(activity_at), 0) FROM worktree_cleanup_activity WHERE platform = ? AND session_id = ?`, s.key.Platform, s.ID).Scan(&at); err != nil {
			return err
		}
		if at+retention.Milliseconds() > m.now().UnixMilli() {
			return fmt.Errorf("recent owner-local session activity")
		}
		count, err := m.d.Store.CountQueuedMessages(ctx, s.key.Platform, s.ID)
		if err != nil {
			return err
		}
		if count != 0 {
			return fmt.Errorf("session has queued work")
		}
		var p platforms.Platform
		for _, adapter := range m.d.Platforms() {
			if string(adapter.ID()) == s.key.Platform {
				p = adapter
				break
			}
		}
		if p == nil {
			return fmt.Errorf("session adapter disappeared")
		}
		reader, ok := p.(platforms.LifecycleReader)
		if !ok {
			return fmt.Errorf("owner cannot verify session lifecycle")
		}
		life, err := reader.SessionLifecycle(ctx, s.ID)
		if err != nil {
			return err
		}
		if life == nil || life.ReadOnly || (life.Status != s.Status) {
			return fmt.Errorf("session lifecycle changed or is externally managed")
		}
		if live := p.LiveStatus(s.ID); live != nil && (live.Status == "busy" || live.PendingPermission || live.PendingQuestion || live.BackgroundTaskCount != 0 || live.BackgroundServiceCount != 0) {
			return fmt.Errorf("session has live work")
		}
		if background, ok := p.(platforms.BackgroundTaskManager); ok {
			snapshot, err := background.BackgroundTasks(ctx, s.ID)
			if err != nil && !errors.Is(err, platforms.ErrUnsupported) {
				return err
			}
			if snapshot != nil && snapshot.Count != 0 {
				return fmt.Errorf("session has background work")
			}
		}
		if queue, ok := p.(platforms.NativeQueue); ok {
			held, err := queue.NativeQueued(ctx, s.ID)
			if err != nil && !errors.Is(err, platforms.ErrUnsupported) {
				return err
			}
			if len(held) != 0 {
				return fmt.Errorf("session has native queued work")
			}
		}
	}
	if seen != len(expected) {
		return fmt.Errorf("owning sessions disappeared")
	}
	return nil
}

func (m *Module) verifyRuntimeInventory(ctx context.Context, path string, expected map[state.Key]int64) error {
	for _, p := range m.d.Platforms() {
		if remote, _ := platforms.SplitPlatformID(string(p.ID())); remote != "" {
			continue
		}
		reader, ok := p.(platforms.RuntimeSessionReader)
		if !ok {
			return fmt.Errorf("owner cannot verify native session inventory")
		}
		rows, err := reader.GetSessionIdentities(ctx)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if !within(path, row.Directory) {
				continue
			}
			updated, ok := expected[state.Key{Platform: string(p.ID()), SessionID: row.ID}]
			if !ok {
				return fmt.Errorf("new native session uses this checkout")
			}
			life, err := reader.GetSessionLifecycle(ctx, row.ID)
			if err != nil {
				return err
			}
			if life.Directory != row.Directory || life.Status == "busy" || life.Status == "waiting" || life.LatestMessageCreated > updated {
				return fmt.Errorf("native session is active or moved")
			}
		}
	}
	return nil
}
