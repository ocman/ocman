package opencode

import "github.com/NoUseFreak/ocman/internal/platforms/opencode/apiv2"

// ResetCaches clears the process-wide HTTP and v2 dialect caches before a
// backend reload, so the next backend never serves the previous one's
// answers. Port discovery is per adapter (Discovery), so a reload's new
// adapter starts empty.
func ResetCaches() {
	catalogCache.reset()
	fetchFailureLogs.reset()
	ocv2.ResetHostCache()
}
