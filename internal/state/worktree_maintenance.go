package state

import (
	"context"
	"sync/atomic"
)

type worktreeLeaseKey struct{}

type worktreeLease struct {
	db        *DB
	active    atomic.Bool
	exclusive bool
}

func (d *DB) holdsWorktreeLease(ctx context.Context, exclusive bool) bool {
	lease, _ := ctx.Value(worktreeLeaseKey{}).(*worktreeLease)
	return lease != nil && lease.db == d && lease.active.Load() && (!exclusive || lease.exclusive)
}

func (d *DB) worktreeLeaseContext(ctx context.Context, exclusive bool, unlock func()) (context.Context, func()) {
	lease := &worktreeLease{db: d, exclusive: exclusive}
	lease.active.Store(true)
	return context.WithValue(ctx, worktreeLeaseKey{}, lease), func() {
		if lease.active.CompareAndSwap(true, false) {
			unlock()
		}
	}
}

// WorktreeActivity excludes checkout cleanup while a mutation is admitted.
// Propagate the returned context through nested mutations to avoid relocking.
func (d *DB) WorktreeActivity(ctx context.Context) (context.Context, func()) {
	if d == nil || d.holdsWorktreeLease(ctx, false) {
		return ctx, func() {}
	}
	d.worktreeMu.RLock()
	return d.worktreeLeaseContext(ctx, false, d.worktreeMu.RUnlock)
}

// WorktreeMaintenance excludes archive changes and admitted session/terminal
// mutations until the safety checks and checkout removal have completed.
func (d *DB) WorktreeMaintenance(ctx context.Context) (context.Context, func()) {
	if d == nil || d.holdsWorktreeLease(ctx, true) {
		return ctx, func() {}
	}
	d.worktreeMu.Lock()
	return d.worktreeLeaseContext(ctx, true, d.worktreeMu.Unlock)
}

type ArchivedSession struct {
	Key
	ArchivedAt, TimeUpdated, SnoozedUntil int64
}

// ArchivedSessionRecords includes real archive timestamps, not just the
// resurface watermark. Snoozing alone must never authorize checkout deletion.
func (d *DB) ArchivedSessionRecords(ctx context.Context) ([]ArchivedSession, error) {
	rows, err := d.reader().QueryContext(ctx, `SELECT platform, session_id, archived_at,
		session_time_updated, snoozed_until FROM archived_session`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ArchivedSession
	for rows.Next() {
		var v ArchivedSession
		if err := rows.Scan(&v.Platform, &v.SessionID, &v.ArchivedAt, &v.TimeUpdated, &v.SnoozedUntil); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
