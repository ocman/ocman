package local

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	gitpkg "github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
)

// ProviderRootForCleanup shares launch identity but never guesses after a
// failed git probe. Launches outside repositories can still work without git.
func ProviderRootForCleanup(ctx context.Context, directory string) (string, error) {
	if _, err := os.Stat(directory); errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("%w: %s", hostsvc.ErrMissingProjectDirectory, directory)
	} else if err != nil {
		return "", err
	}
	root, err := gitpkg.ResolveMainRepoRoot(ctx, directory)
	if err == nil {
		return root, nil
	}
	if !errors.Is(err, gitpkg.ErrNotARepo) {
		return "", err
	}
	return ProviderRoot(ctx, directory)
}

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
	if err := m.Runtime.Probe(ctx, inst); err != nil {
		return false, err
	}
	if !m.Runtime.Current(inst) {
		return false, nil
	}
	token := rand.Text()
	idle, err = m.Runtime.DrainIdle(ctx, inst, token)
	if err != nil {
		return false, errors.Join(err, m.resumeIdle(ctx, inst, token))
	}
	if !idle {
		return false, nil
	}
	if err := m.Runtime.Stop(ctx, inst); err != nil {
		return false, errors.Join(err, m.resumeIdle(ctx, inst, token))
	}
	detach()
	return true, m.State.DeleteManagedProvider(ctx, m.Runtime.PlatformID, root)
}

func (m *ProviderManager) resumeIdle(ctx context.Context, inst *ocruntime.Instance, token string) error {
	rollback, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return m.Runtime.Resume(rollback, inst, token)
}
