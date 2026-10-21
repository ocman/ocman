package platforms

import "context"

// SessionCacheStarter owns any native discovery cache required by ingestion.
// Application code never receives the native database backing that cache.
type SessionCacheStarter interface {
	StartSessionCache(context.Context, func(string) bool)
}
