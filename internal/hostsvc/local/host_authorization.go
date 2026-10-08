package local

import "github.com/NoUseFreak/ocman/internal/ocruntime"

// Authorization is process-local proof, never inferred from a durable handle.
func (h *Host) authorizeInstance(root string, inst *ocruntime.Instance) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.instances[root] = inst
	h.authorized[root] = inst.Endpoint
}

func (h *Host) revokeInstanceAuthorization(root string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.authorized, root)
}

func (h *Host) authorizedEndpoint(root string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.authorized[root]
}
