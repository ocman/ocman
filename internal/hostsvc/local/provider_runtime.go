package local

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	gitpkg "github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
	"github.com/NoUseFreak/ocman/internal/state"
)

// ProviderManager is wired into hostsvc/local.Deps.Runtimes by provider
// registration. Persisted rows are always probed, including the first ensure
// after a dashboard restart. Replacement returns a new endpoint; the caller
// must create a fresh provider adapter rather than carrying over its cursor.
type ProviderManager struct {
	Runtime *ocruntime.ProviderRuntime
	State   *state.DB
	// OnDrain is called before a supervisor is drained for replacement,
	// with whether it still speaks a supported protocol, so the dashboard
	// keeps a route that answers its pending prompts until it settles.
	OnDrain func(root, endpoint string, compatible bool)
	mu      sync.Mutex
	roots   map[string]*sync.Mutex
}

func (m *ProviderManager) Ensure(ctx context.Context, directory string) (*hostsvc.EnsureRuntimeResult, error) {
	root, err := ProviderRoot(ctx, directory)
	if err != nil {
		return nil, err
	}
	return m.ensureRoot(ctx, root)
}

// ProviderRoot keys a directory's supervisor: the main checkout, so every
// linked worktree shares it, or the canonical directory outside a repository
// (Claude sessions need no repository).
func ProviderRoot(ctx context.Context, directory string) (string, error) {
	if root, err := gitpkg.ResolveMainRepoRoot(ctx, directory); err == nil {
		return root, nil
	}
	if !filepath.IsAbs(directory) {
		return "", fmt.Errorf("provider directory must be absolute: %q", directory)
	}
	root, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return "", fmt.Errorf("provider directory %q is not a directory", directory)
	}
	return root, nil
}

// Recover reattaches a persisted supervisor after a dashboard restart
// without launching one: a healthy row is reused, an out-of-range one is
// drained and replaced (AD-5), and a dead one is left for the next Ensure.
func (m *ProviderManager) Recover(ctx context.Context, root string) (*hostsvc.EnsureRuntimeResult, bool, error) {
	if m.Runtime == nil || m.State == nil {
		return nil, false, fmt.Errorf("provider manager requires runtime and state")
	}
	row, exists, err := m.State.GetManagedProvider(ctx, m.Runtime.ProviderID, root)
	if err != nil || !exists {
		return nil, false, err
	}
	inst := &ocruntime.Instance{Endpoint: row.Endpoint, Kind: row.Kind, ID: row.RuntimeID, PID: row.PID, RepoRoot: root}
	err = m.Runtime.Probe(ctx, inst)
	if err == nil && m.Runtime.Current(inst) {
		return &hostsvc.EnsureRuntimeResult{Endpoint: inst.Endpoint, RepoRoot: root, Runtime: *inst}, true, nil
	}
	var skew *providerprotocol.VersionError
	if err != nil && !errors.As(err, &skew) {
		return nil, false, nil
	}
	result, err := m.ensureRoot(ctx, root)
	return result, err == nil, err
}

func (m *ProviderManager) ensureRoot(ctx context.Context, root string) (*hostsvc.EnsureRuntimeResult, error) {
	if m.Runtime == nil || m.State == nil {
		return nil, fmt.Errorf("provider manager requires runtime and state")
	}
	// Per root: a supervisor draining for an upgrade must not block others.
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
	defer lock.Unlock()
	r := m.Runtime
	row, exists, err := m.State.GetManagedProvider(ctx, r.ProviderID, root)
	if err != nil {
		return nil, err
	}
	if exists {
		inst := &ocruntime.Instance{Endpoint: row.Endpoint, Kind: row.Kind, ID: row.RuntimeID, PID: row.PID, RepoRoot: root}
		err = r.Probe(ctx, inst)
		if err == nil && r.Current(inst) {
			return &hostsvc.EnsureRuntimeResult{Endpoint: inst.Endpoint, RepoRoot: root, Runtime: *inst}, nil
		}
		// An out-of-range protocol or an earlier launch configuration (another
		// CLI or Claude config) is replaced, but only once it has settled.
		var skew *providerprotocol.VersionError
		replace := err == nil || errors.As(err, &skew)
		if replace {
			if m.OnDrain != nil {
				m.OnDrain(root, inst.Endpoint, err == nil)
			}
			// /drain is a version-independent control operation; of an
			// out-of-range supervisor only it and the version-independent
			// prompt and abort routes are used (provider.NewDraining).
			for {
				idle, err := r.Drain(ctx, inst)
				if err != nil {
					return nil, err
				}
				if idle {
					break
				}
				timer := time.NewTimer(time.Second)
				select {
				case <-ctx.Done():
					timer.Stop()
					return nil, ctx.Err()
				case <-timer.C:
				}
			}
			if err := r.Stop(ctx, inst); err != nil {
				return nil, err
			}
		}
		if !replace {
			alive, aliveErr := r.Alive(ctx, inst)
			if aliveErr != nil {
				return nil, aliveErr
			}
			if alive {
				return nil, err
			}
		}
		// Only a confirmed dead handle or a completed drain permits replacement.
		if err := m.State.DeleteManagedProvider(ctx, r.ProviderID, root); err != nil {
			return nil, err
		}
	}
	inst, err := r.Launch(ctx, ocruntime.LaunchSpec{RepoRoot: root})
	if err != nil {
		return nil, err
	}
	// Persist before readiness so a timeout/restart still finds and probes the
	// launched supervisor rather than silently creating another one.
	if err := m.State.UpsertManagedProvider(ctx, r.ProviderID, root, state.ManagedInstance{Endpoint: inst.Endpoint, Kind: inst.Kind, RuntimeID: inst.ID, PID: inst.PID, LaunchedAt: time.Now()}); err != nil {
		_ = r.Stop(ctx, inst)
		return nil, err
	}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		if err := r.Probe(ctx, inst); err == nil {
			return &hostsvc.EnsureRuntimeResult{Endpoint: inst.Endpoint, RepoRoot: root, Runtime: *inst, Launched: true}, nil
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-deadline.C:
			timer.Stop()
			return nil, fmt.Errorf("provider supervisor did not become ready")
		case <-timer.C:
		}
	}
}
