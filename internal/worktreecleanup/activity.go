package worktreecleanup

import (
	"context"
	"fmt"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
)

// BeginSession is called only for explicit mutations, never browsing/polling.
// The returned lease spans delivery so cleanup cannot race an admitted turn.
func (m *Module) BeginSession(ctx context.Context, p platforms.Platform, id string) (context.Context, func(), error) {
	if m.d.Store == nil {
		return ctx, func() {}, nil
	}
	ctx, release := m.d.Store.WorktreeActivity(ctx)
	if err := m.touch(ctx, state.Key{Platform: string(p.ID()), SessionID: id}); err != nil {
		release()
		return ctx, nil, err
	}
	owner, _ := platforms.SplitPlatformID(string(p.ID()))
	owner = state.NormalizeRemoteID(owner)
	var pending bool
	err := m.d.Store.SQL().QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM worktree_cleanup_checkout WHERE owner = ?)`, owner).Scan(&pending)
	if err != nil {
		release()
		return ctx, nil, err
	}
	if !pending {
		return ctx, release, nil
	}
	detail, err := p.Session(ctx, id, 1, 0)
	if err == nil && detail != nil && detail.Session != nil {
		err = m.restore(ctx, owner, detail.Session.Directory)
	} else if err == nil {
		err = fmt.Errorf("cannot resolve session checkout for recovery")
	}
	if err != nil {
		release()
		return ctx, nil, err
	}
	return ctx, release, nil
}

// BeginDirectory protects host-side launches/terminals and refreshes every
// known session using that checkout. Restoring never creates a new session.
func (m *Module) BeginDirectory(ctx context.Context, dir string) (context.Context, func(), error) {
	return m.BeginOwnerDirectory(ctx, state.LocalRemoteID, dir)
}

func (m *Module) BeginOwnerDirectory(ctx context.Context, owner, dir string) (context.Context, func(), error) {
	owner = state.NormalizeRemoteID(owner)
	if m.d.Store == nil {
		return ctx, func() {}, nil
	}
	ctx, release := m.d.Store.WorktreeActivity(ctx)
	if err := m.restore(ctx, owner, dir); err != nil {
		release()
		return ctx, nil, err
	}
	for _, p := range m.d.Platforms() {
		if remote, _ := platforms.SplitPlatformID(string(p.ID())); state.NormalizeRemoteID(remote) != owner {
			continue
		}
		sessions, err := p.Sessions(ctx, dir, 0)
		if err != nil {
			// An unrelated unavailable adapter must not block terminal/session
			// creation. Its incomplete inventory already prevents cleanup.
			continue
		}
		for _, s := range sessions {
			if !within(dir, s.Directory) {
				continue
			}
			if err := m.touch(ctx, state.Key{Platform: string(p.ID()), SessionID: s.ID}); err != nil {
				release()
				return ctx, nil, err
			}
		}
	}
	return ctx, release, nil
}

func (m *Module) restore(ctx context.Context, owner, dir string) error {
	checkpoint, err := m.checkpoint(ctx, owner, dir)
	if err != nil || checkpoint == nil {
		return err
	}
	host, ok := m.d.Router().LookupRemote(owner)
	if !ok {
		return platforms.ErrRemoteOffline
	}
	maintenance, ok := host.(hostsvc.WorktreeMaintenance)
	if !ok {
		return fmt.Errorf("owner cannot restore cleaned worktree")
	}
	if err := maintenance.RestoreWorktree(ctx, *checkpoint); err != nil {
		return err
	}
	return m.forget(ctx, owner, checkpoint.Path)
}
