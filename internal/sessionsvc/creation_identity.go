package sessionsvc

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/owned"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/google/uuid"
)

// WithSessionCatalogue installs metadata and legacy-link compatibility before
// serving. Native bindings and pre-cutover receipt handling belong to owned.
func (s *Service) WithSessionCatalogue(store *sessioncatalog.Store, ownerID string) {
	s.identities = owned.NewIdentityGateway(store, ownerID, func() owned.IdentityRegistry { return s.registry }, s.pickAdapter, validation)
}

type creationAttempt = owned.CreationAttempt
type CreationUncertainError = owned.LegacyCreationUncertainError

func prepareCreationIdentity(req *platforms.CreateSessionRequest, routineID string) error {
	if req.Creation == nil {
		req.Creation = &platforms.CreationIdentity{RequestKey: uuid.NewString(), Origin: string(sessioncatalog.User)}
	} else {
		copy := *req.Creation
		req.Creation = &copy
	}
	if routineID != "" {
		if req.Creation.Origin != string(sessioncatalog.User) && (req.Creation.Origin != string(sessioncatalog.Routine) || req.Creation.OriginID != routineID) {
			return validation("creation origin does not match routine")
		}
		req.Creation.Origin, req.Creation.OriginID = string(sessioncatalog.Routine), routineID
	}
	return nil
}

func (s *Service) reserveCreation(ctx context.Context, p platforms.Platform, req platforms.CreateSessionRequest, rules []platforms.PermissionRule, operation ...any) (*creationAttempt, *platforms.CreateSessionResponse, error) {
	return s.identities.Reserve(ctx, p, req, rules, operation...)
}
