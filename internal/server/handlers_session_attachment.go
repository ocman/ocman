package server

import (
	"time"

	"github.com/NoUseFreak/ocman/internal/composerattachments"
)

func composerAttachmentRoot() string { return composerattachments.Root() }

// ponytail: seven-day age cap; reference counting if users need older uploads.
const composerAttachmentTTL = 7 * 24 * time.Hour

func sweepComposerAttachments(root string, ttl time.Duration) int {
	return composerattachments.Sweep(root, ttl)
}
