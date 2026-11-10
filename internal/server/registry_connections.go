package server

import (
	"context"
	"github.com/ocman/ocman/internal/platforms"
)

func (r registryRef) PlatformForCanonicalSource(ctx context.Context, owner, kind, source string) (platforms.Platform, platforms.ID, bool) {
	return r.s.registry.PlatformForCanonicalSource(ctx, owner, kind, source)
}

func (r registryRef) KeyFor(p platforms.Platform) (platforms.ID, bool) { return r.s.registry.KeyFor(p) }

func (r registryRef) ResolveLegacyScope(ctx context.Context, scope string) (platforms.ID, error) {
	return r.s.registry.ResolveLegacyScope(ctx, scope)
}
