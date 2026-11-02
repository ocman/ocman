package server

import "github.com/ocman/ocman/internal/platforms/platformtest"

// fakePlatform is the shared configurable Platform. Tests wire it into a
// Registry alongside the real OpenCode adapter to exercise cross-platform
// handler paths without a second adapter with its own data store.
type fakePlatform = platformtest.Fake
