package sessionsvc

import "github.com/NoUseFreak/ocman/internal/platforms"

// sessionCacheKey is owner-qualified; canonical adapters use Ocman IDs.
type sessionCacheKey struct {
	platform platforms.ID
	id       string
}
