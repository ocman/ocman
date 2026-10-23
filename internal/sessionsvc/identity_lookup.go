package sessionsvc

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/owned"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
)

// LookupSessionIdentity is the legacy-link transport entry point. All physical
// source and native-reference handling stays in the owning integration.
func (s *Service) LookupSessionIdentity(ctx context.Context, platformID string, req platforms.SessionIdentityRequest) (*platforms.SessionIdentity, error) {
	if (req.ID == "") == (req.NativeID == "") {
		return nil, validation("specify id or nativeId")
	}
	if req.ID != "" && !sessioncatalog.IsID(req.ID) {
		return nil, validation("invalid Ocman session ID")
	}
	if s.identities == nil {
		return nil, platforms.ErrHistoryUnavailable
	}
	return s.identities.Lookup(ctx, platformID, req)
}

func (s *Service) CoreSessionReference(info *platforms.SessionIdentity) string {
	return s.identities.CoreReference(info)
}

func (s *Service) CanonicalSessionReferences(scope string) bool {
	if s == nil || s.registry == nil {
		return false
	}
	if s.registry.CanonicalSessionIDs() {
		return true
	}
	if scope != "" {
		p, ok := s.registry.Get(platforms.ID(scope))
		return ok && owned.CanonicalSessionIDs(p)
	}
	for _, p := range s.registry.Platforms() {
		if owned.CanonicalSessionIDs(p) {
			return true
		}
	}
	return false
}

func (s *Service) LookupSessionReference(ctx context.Context, scope, reference string) (*platforms.SessionIdentity, error) {
	if s.identities == nil {
		return nil, platforms.ErrHistoryUnavailable
	}
	return s.identities.LookupReference(ctx, scope, reference)
}

func (s *Service) PublicSessionReferences(ctx context.Context, scope string, refs []string) (map[string]string, error) {
	if s.identities == nil {
		return nil, platforms.ErrHistoryUnavailable
	}
	return s.identities.ReferenceMapping(ctx, scope, refs)
}
