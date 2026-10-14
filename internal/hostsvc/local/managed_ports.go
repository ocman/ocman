package local

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
)

// ManagedOpenCodePorts enumerates only tracked runtimes, never external processes.
// Persisted endpoints must pass the same identity probe used by session creation.
func (h *Host) ManagedOpenCodePorts(ctx context.Context) map[string]string {
	ports := map[string]string{}
	if !h.opencodeEnabled() {
		return ports
	}
	roots := map[string]bool{}
	if h.store != nil {
		instances, err := h.store.List(ctx)
		if err != nil {
			return nil
		}
		for root := range instances {
			roots[root] = true
		}
	}
	h.mu.Lock()
	for root := range h.instances {
		roots[root] = true
	}
	h.mu.Unlock()
	for root := range roots {
		if ctx.Err() != nil {
			return nil
		}
		v, err, _ := h.sf.Do(root, func() (any, error) {
			if stopped, err := h.reconcileStopped(ctx, root, false); err != nil {
				return nil, err
			} else if stopped {
				return nil, ocruntime.ErrProbeUnreachable
			}
			inst := h.reuseCandidate(ctx, root)
			if inst == nil {
				return nil, ocruntime.ErrProbeUnreachable
			}
			if err := h.runtime.Probe(ctx, inst); err != nil {
				if !ambiguousProbe(err) {
					h.revokeInstanceAuthorization(root)
				}
				return nil, err
			}
			h.authorizeInstance(root, inst)
			return &hostsvc.EnsureRuntimeResult{Endpoint: inst.Endpoint, RepoRoot: root, Runtime: *inst}, nil
		})
		result, _ := v.(*hostsvc.EnsureRuntimeResult)
		if result == nil && err != nil && ambiguousProbe(err) {
			result = &hostsvc.EnsureRuntimeResult{Endpoint: h.authorizedEndpoint(root)}
		}
		if result != nil {
			if port := result.Port(); port != "" {
				ports[root] = port
			}
		}
	}
	return ports
}
