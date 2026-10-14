package local

import "github.com/NoUseFreak/ocman/internal/ocruntime"

// Authorization is process-local proof, never inferred from a durable handle.
func (h *Host) authorizeInstance(root string, inst *ocruntime.Instance) {
	h.mu.Lock()
	h.authorizeInstanceLocked(root, inst)
	h.mu.Unlock()
	h.runtimeInventoryChanged()
}

func (h *Host) authorizeInstanceLocked(root string, inst *ocruntime.Instance) {
	h.instances[root] = inst
	h.authorized[root] = inst.Endpoint
	h.generations[root]++
}

func (h *Host) revokeInstanceAuthorization(root string) {
	h.mu.Lock()
	delete(h.authorized, root)
	h.generations[root]++
	h.mu.Unlock()
	h.runtimeInventoryChanged()
}

func (h *Host) runtimeInventoryChanged() {
	if h.deps.RuntimeInventoryChanged != nil {
		h.deps.RuntimeInventoryChanged()
	}
}

func (h *Host) authorizedEndpoint(root string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.authorized[root]
}
