package owned

import (
	"context"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/identity"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

func (g *IdentityGateway) LookupReference(ctx context.Context, scope, reference string) (*platforms.SessionIdentity, error) {
	if identity.IsPendingReference(reference) {
		return nil, platforms.ErrPlatformUnreachable
	}
	request := platforms.SessionIdentityRequest{ID: reference}
	if !sessioncatalog.IsID(reference) {
		request = platforms.SessionIdentityRequest{NativeID: reference}
	}
	return g.Lookup(ctx, scope, request)
}
