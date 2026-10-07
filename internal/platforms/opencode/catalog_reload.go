package opencode

// InvalidateCatalogsForPort makes post-reload reads fetch the owner's fresh catalogs.
func InvalidateCatalogsForPort(port string) {
	catalogCache.invalidatePort(port)
}
