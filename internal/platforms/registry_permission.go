package platforms

import (
	"context"
	"strings"

	"github.com/ocman/ocman/internal/platforms/identity"
)

// PermissionPlatform resolves permission callbacks that still carry a native
// kind as well as callbacks carrying an exact connection key. Only a canonical
// session may disambiguate a kind; explicit connections never fall back.
func (r *Registry) PermissionPlatform(ctx context.Context, scope ID, session string) (Platform, ID, bool) {
	if p, ok := r.Get(scope); ok {
		return p, scope, true
	}
	remote, _ := SplitPlatformID(string(scope))
	if remote != "" || strings.HasPrefix(string(scope), "cnx_") || !identity.IsSessionID(session) {
		return nil, "", false
	}
	p, ok := r.PlatformForSession(ctx, session)
	if !ok || p.ID() != scope || !CanonicalAdapter(p) {
		return nil, "", false
	}
	key, ok := r.KeyFor(p)
	return p, key, ok
}
