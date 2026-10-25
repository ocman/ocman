package claudeprovider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// The child cap is machine-wide (spike §1, AD-3), while each project has its
// own supervisor. Options.SlotDir holds one lock file per slot: a running
// child holds a slot's flock until it is stopped or reaped. A supervisor that
// finds every slot taken and has no idle child of its own holds a wait-* lock
// file; supervisors with idle children evict one when they see it.

const slotPoll = 200 * time.Millisecond

// HelperProcs bounds the short one-shot claude calls (initialize probes for
// catalogs and the account, the Claude judge, worktree naming) machine-wide.
// They have their own slots beside the MaxProcs pooled children: a judge
// must never wait behind sessions that wait on its verdict.
const HelperProcs = 2

// trySlot takes the first free prefix-N lock under dir. Contention is
// (nil, false, nil); any other failure is a persistent I/O error the caller
// must surface rather than wait on forever.
func trySlot(dir, prefix string, n int) (func(), bool, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, false, fmt.Errorf("claude process slots: %w", err)
	}
	for i := range n {
		f, err := os.OpenFile(filepath.Join(dir, fmt.Sprintf("%s-%d", prefix, i)), os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return nil, false, fmt.Errorf("claude process slots: %w", err)
		}
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			_ = f.Close()
			if errors.Is(err, syscall.EWOULDBLOCK) {
				continue
			}
			return nil, false, fmt.Errorf("claude process slots: %w", err)
		}
		return func() { _ = f.Close() }, true, nil
	}
	return nil, false, nil
}

// takeSlotLocked reserves a slot for a new child; release is idempotent.
func (l *live) takeSlotLocked() (release func(), ok bool, err error) {
	if l.opts.SlotDir == "" {
		return func() {}, l.children < l.opts.MaxProcs, nil
	}
	return trySlot(l.opts.SlotDir, "slot", l.opts.MaxProcs)
}

// HelperSlot waits for one of the HelperProcs helper slots under dir (empty
// dir: unbounded) until ctx ends; the caller runs its claude call, then
// releases. A slot directory that cannot be used is an error, not a wait.
func HelperSlot(ctx context.Context, dir string) (release func(), err error) {
	if dir == "" {
		return func() {}, nil
	}
	for {
		release, ok, err := trySlot(dir, "helper", HelperProcs)
		if err != nil || ok {
			return release, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(slotPoll):
		}
	}
}

// waitSlotLocked blocks until a slot may have been freed. With machine slots
// it announces the wait and polls, since another process frees the slot.
func (l *live) waitSlotLocked(ctx context.Context, waiter **os.File) {
	if l.opts.SlotDir == "" {
		l.slots.Wait()
		return
	}
	if *waiter == nil {
		if f, err := os.CreateTemp(l.opts.SlotDir, "wait-"); err == nil {
			if syscall.Flock(int(f.Fd()), syscall.LOCK_EX) == nil {
				*waiter = f
			} else {
				_ = os.Remove(f.Name())
				_ = f.Close()
			}
		}
	}
	l.mu.Unlock()
	select {
	case <-ctx.Done():
	case <-time.After(slotPoll):
	}
	l.mu.Lock()
}

func dropWaiter(f *os.File) {
	if f != nil {
		_ = os.Remove(f.Name())
		_ = f.Close()
	}
}

// othersWaiting reports a live waiter; stale files of exited waiters are removed.
func othersWaiting(dir string) bool {
	paths, _ := filepath.Glob(filepath.Join(dir, "wait-*"))
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		busy := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil
		if !busy {
			_ = os.Remove(path)
		}
		_ = f.Close()
		if busy {
			return true
		}
	}
	return false
}

// yieldSlots evicts one idle child whenever another supervisor waits for a slot.
func (l *live) yieldSlots(ctx context.Context) {
	if l.opts.SlotDir == "" {
		return
	}
	ticker := time.NewTicker(slotPoll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		l.mu.Lock()
		idle := false
		for _, p := range l.procs {
			idle = idle || (!p.busy && len(p.tasks) == 0)
		}
		idle = idle && !l.exitingLocked()
		l.mu.Unlock()
		if !idle || !othersWaiting(l.opts.SlotDir) {
			continue
		}
		l.mu.Lock()
		for _, p := range l.procs {
			if !p.busy && len(p.tasks) == 0 {
				l.stopLocked(p)
				break
			}
		}
		l.mu.Unlock()
	}
}
