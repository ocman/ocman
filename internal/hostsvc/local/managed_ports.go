package local

import (
	"context"
	"sync"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
)

type inventoryCandidate struct {
	instance         ocruntime.Instance
	generation       uint64
	cold, authorized bool
}

// ManagedOpenCodePorts observes tracked runtimes without joining mutation
// flights or holding locks across callbacks/probes. Generations fence stale
// reads, including persisted rows read across a stop or replacement.
func (h *Host) ManagedOpenCodePorts(ctx context.Context) map[string]string {
	if !h.opencodeEnabled() {
		return map[string]string{}
	}
	if ctx.Err() != nil {
		return nil
	}
	h.mu.Lock()
	generations := make(map[string]uint64, len(h.generations))
	for root, generation := range h.generations {
		generations[root] = generation
	}
	candidates := make(map[string]inventoryCandidate, len(h.instances))
	for root, inst := range h.instances {
		candidates[root] = inventoryCandidate{instance: *inst, generation: generations[root], authorized: h.authorized[root] == inst.Endpoint}
	}
	h.mu.Unlock()
	if h.store != nil {
		instances, err := h.store.List(ctx)
		if err != nil {
			return nil
		}
		for root, inst := range instances {
			if _, warm := candidates[root]; !warm {
				candidates[root] = inventoryCandidate{instance: ocruntime.Instance{Endpoint: inst.Endpoint, Kind: inst.Kind, ID: inst.RuntimeID, PID: inst.PID, RepoRoot: root}, generation: generations[root], cold: true}
			}
		}
	}
	for root, candidate := range candidates {
		if ctx.Err() != nil {
			return nil
		}
		h.mu.Lock()
		current := h.currentInventoryCandidateLocked(root, candidate)
		h.mu.Unlock()
		if !current {
			continue
		}
		blocked, err := h.inventoryStopFence(ctx, root, candidate)
		if err != nil {
			return nil
		}
		if !blocked {
			err = h.runtime.Probe(ctx, &candidate.instance)
		}
		h.mu.Lock()
		if h.currentInventoryCandidateLocked(root, candidate) {
			switch {
			case blocked, err != nil && !ambiguousProbe(err):
				delete(h.authorized, root)
				h.generations[root]++
			case err == nil:
				h.authorizeInstanceLocked(root, &candidate.instance)
			}
		}
		h.mu.Unlock()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	ports := map[string]string{}
	for root, endpoint := range h.authorized {
		inst := h.instances[root]
		if h.stopped[root] || inst == nil || inst.Endpoint != endpoint {
			continue
		}
		if port := (&hostsvc.EnsureRuntimeResult{Endpoint: endpoint}).Port(); port != "" {
			ports[root] = port
		}
	}
	return ports
}

func (h *Host) currentInventoryCandidateLocked(root string, candidate inventoryCandidate) bool {
	if h.mutating[root] || h.stopped[root] || h.generations[root] != candidate.generation {
		return false
	}
	current := h.instances[root]
	return (candidate.cold && current == nil) || (!candidate.cold && current != nil && *current == candidate.instance)
}

// These are read-only durable-state queries. Inventory never reconciles or
// stops a pending replacement, and a cold handle is never routing proof.
func (h *Host) inventoryStopFence(ctx context.Context, root string, candidate inventoryCandidate) (bool, error) {
	if h.deps.ReplacementStopped != nil {
		stopped, err := h.deps.ReplacementStopped(ctx, root)
		if err != nil || stopped {
			return stopped, err
		}
	}
	if h.deps.ReplacementStopping != nil {
		pending, err := h.deps.ReplacementStopping(ctx, root)
		if err != nil {
			return false, err
		}
		if pending != nil {
			return !candidate.authorized || *pending != candidate.instance, nil
		}
	}
	return false, nil
}

func (h *Host) beginRuntimeMutation(root string) func() {
	value, _ := h.runtimeLocks.LoadOrStore(root, &sync.Mutex{})
	lock := value.(*sync.Mutex)
	lock.Lock()
	h.mu.Lock()
	h.generations[root]++
	h.mutating[root] = true
	h.mu.Unlock()
	return func() {
		h.mu.Lock()
		h.generations[root]++
		delete(h.mutating, root)
		h.mu.Unlock()
		lock.Unlock()
	}
}
