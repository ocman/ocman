package factorymod

import (
	"github.com/ocman/ocman/internal/platforms"
	"strings"
)

// A phase's explicit account choice overrides the Epic's default for new sessions.
func factoryModelPlatform(fallback, model string) string {
	if prefix, _ := platforms.SplitModel(model); strings.HasPrefix(string(prefix), "cnx_") {
		return string(prefix)
	}
	return fallback
}
