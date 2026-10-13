package local

import (
	"context"
	"errors"
	"sync"

	"github.com/NoUseFreak/ocman/internal/ocruntime"
)

func (m *ProviderManager) lockRoot(root string) func() {
	m.mu.Lock()
	if m.roots == nil {
		m.roots = map[string]*sync.Mutex{}
	}
	lock := m.roots[root]
	if lock == nil {
		lock = &sync.Mutex{}
		m.roots[root] = lock
	}
	m.mu.Unlock()
	lock.Lock()
	return lock.Unlock
}

// CullIdle rechecks eligibility under the same lock used by Ensure. Admission
// is fenced by the supervisor before stopping, so a racing turn is preserved.
func (m *ProviderManager) CullIdle(ctx context.Context, root string, check func(context.Context) (bool, error), detach func()) (bool, error) {
	unlock := m.lockRoot(root)
	defer unlock()
	row, exists, err := m.State.GetManagedProvider(ctx, m.Runtime.PlatformID, root)
	if err != nil || !exists {
		return false, err
	}
	idle, err := check(ctx)
	if err != nil || !idle {
		return false, err
	}
	inst := &ocruntime.Instance{Endpoint: row.Endpoint, Kind: row.Kind, ID: row.RuntimeID, PID: row.PID, RepoRoot: root}
	idle, err = m.Runtime.DrainIdle(ctx, inst)
	if err != nil || !idle {
		return false, err
	}
	if err := m.Runtime.Stop(ctx, inst); err != nil {
		return false, errors.Join(err, m.Runtime.Resume(ctx, inst))
	}
	detach()
	return true, m.State.DeleteManagedProvider(ctx, m.Runtime.PlatformID, root)
}
