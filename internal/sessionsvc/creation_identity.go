package sessionsvc

import (
	"github.com/google/uuid"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/owned"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

// WithSessionCatalogue installs metadata and legacy-link compatibility before
// serving. Native bindings and pre-cutover receipt handling belong to owned.
func (s *Service) WithSessionCatalogue(store *sessioncatalog.Store, ownerID string) {
	s.identities = owned.NewIdentityGateway(store, ownerID, func() owned.IdentityRegistry { return s.registry }, s.identityReadAdapter, validation)
}

func prepareCreationIdentity(req *platforms.CreateSessionRequest, routineID string) error {
	if req.Creation == nil {
		req.Creation = &platforms.CreationIdentity{Origin: string(sessioncatalog.User)}
	} else {
		copy := *req.Creation
		req.Creation = &copy
	}
	// Partial metadata from MCP and host callbacks still needs a receipt key
	// before named-connection admission, which precedes the owned adapter.
	if req.Creation.RequestKey == "" {
		req.Creation.RequestKey = uuid.NewString()
	}
	if routineID != "" {
		if req.Creation.Origin != string(sessioncatalog.User) && (req.Creation.Origin != string(sessioncatalog.Routine) || req.Creation.OriginID != routineID) {
			return validation("creation origin does not match routine")
		}
		req.Creation.Origin, req.Creation.OriginID = string(sessioncatalog.Routine), routineID
	}
	return nil
}
