package owned

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
)

// IdentityRegistry is the routing contract needed by the legacy-link gateway.
type IdentityRegistry = platforms.SessionRegistry

// IdentityGateway keeps pre-cutover identity and receipt compatibility inside
// the platform integration. Core services never resolve native execution keys.
type IdentityGateway struct {
	store    *sessioncatalog.Store
	owner    string
	registry func() IdentityRegistry
	pick     func(context.Context, string) (platforms.Platform, error)
	invalid  func(string) error
}

func NewIdentityGateway(store *sessioncatalog.Store, owner string, registry func() IdentityRegistry, pick func(context.Context, string) (platforms.Platform, error), invalid func(string) error) *IdentityGateway {
	return &IdentityGateway{store: store, owner: owner, registry: registry, pick: pick, invalid: invalid}
}

// CanonicalSessionIDs is an integration capability, not a platform identity.
func CanonicalSessionIDs(p platforms.Integration) bool {
	return platforms.CanonicalAdapter(p)
}

func (*Adapter) CanonicalSessionIDs() bool { return true }

// CoreReference retains only test/legacy raw-adapter compatibility. Registered
// canonical adapters always return their Ocman identity, including on remotes.
func (g *IdentityGateway) CoreReference(info *platforms.SessionIdentity) string {
	if info == nil {
		return ""
	}
	if g == nil {
		return info.ID
	}
	if g.registry().CanonicalSessionIDs() {
		return info.ID
	}
	if p, ok := g.registry().Get(platforms.ID(info.Platform)); ok && CanonicalSessionIDs(p) {
		return info.ID
	}
	return info.NativeID
}
