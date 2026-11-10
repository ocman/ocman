package opencode

// InvalidateCatalogsForPort makes post-reload reads fetch the owner's fresh catalogs.
func (h *httpState) InvalidateCatalogsForPort(port string) {
	h.cache().invalidatePort(port)
	fetchFailureLogs.invalidatePort(port)
}
