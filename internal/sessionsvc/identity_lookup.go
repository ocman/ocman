package sessionsvc

import (
	"context"
	"errors"
	"fmt"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
)

// LookupSessionIdentity resolves only an explicit identity mode. There is no
// native-ID fallback after an unknown or disconnected Ocman identity.
func (s *Service) LookupSessionIdentity(ctx context.Context, platformID string, req platforms.SessionIdentityRequest) (*platforms.SessionIdentity, error) {
	if (req.ID == "") == (req.NativeID == "") {
		return nil, validation("specify id or nativeId")
	}
	if req.ID != "" && !sessioncatalog.IsID(req.ID) {
		return nil, validation("invalid Ocman session ID")
	}
	if s.identities == nil || s.identities.store == nil {
		return nil, fmt.Errorf("%w: session catalogue unavailable", platforms.ErrHistoryUnavailable)
	}
	var record sessioncatalog.Session
	if req.ID != "" {
		var err error
		record, err = s.identities.store.Get(ctx, req.ID)
		if err != nil && !errors.Is(err, sessioncatalog.ErrNotFound) {
			return nil, err
		}
		if platformID == "" && err == nil {
			if record.OwnerID == s.identities.ownerID {
				platformID = record.Platform
			} else {
				for _, p := range s.registry.Platforms() {
					resolver, ok := p.(platforms.SessionIdentityResolver)
					_, base := platforms.SplitPlatformID(string(p.ID()))
					if ok && resolver.SessionOwnerID() == record.OwnerID && base == record.Platform {
						platformID = string(p.ID())
						break
					}
				}
				if platformID == "" {
					return nil, platforms.ErrPlatformUnreachable
				}
			}
		}
		if platformID == "" && err != nil {
			return nil, platforms.ErrNotFound
		}
	}
	if req.NativeID != "" && platformID == "" {
		p, ok := s.registry.PlatformForSession(ctx, req.NativeID)
		if !ok {
			return nil, platforms.ErrNotFound
		}
		platformID = string(p.ID())
	}
	p, err := s.pickAdapter(ctx, platformID)
	if err != nil {
		return nil, platforms.ErrPlatformUnreachable
	}
	if remote, base := platforms.SplitPlatformID(string(p.ID())); remote != "" {
		resolver, ok := p.(platforms.SessionIdentityResolver)
		if !ok {
			return nil, platforms.ErrUnsupported
		}
		info, err := resolver.SessionIdentity(ctx, req)
		if err != nil {
			return nil, err
		}
		if info == nil || info.OwnerID != resolver.SessionOwnerID() || info.OwnerID != remote || info.Platform != base || info.OwnerID == s.identities.ownerID ||
			(req.ID != "" && info.ID != req.ID) || (req.NativeID != "" && info.NativeID != req.NativeID) {
			return nil, sessioncatalog.ErrBindingConflict
		}
		if err := s.identities.store.Mirror(ctx, identityRecord(info)); err != nil {
			return nil, err
		}
		copy := *info
		copy.Platform = string(p.ID())
		return &copy, nil
	}
	if req.ID != "" {
		if record.ID == "" || record.OwnerID != s.identities.ownerID || record.Platform != string(p.ID()) {
			return nil, platforms.ErrNotFound
		}
	} else {
		record, err = s.catalogueParent(ctx, p, req.NativeID)
		if errors.Is(err, sessioncatalog.ErrNotFound) {
			return nil, platforms.ErrNotFound
		}
		if err != nil {
			return nil, err
		}
	}
	if record.CreationState != sessioncatalog.Ready {
		return nil, sessioncatalog.ErrCreating
	}
	source, err := physicalSource(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("%w: source identity unavailable", platforms.ErrHistoryUnavailable)
	}
	if record.SourceKey != source.SourceKey {
		return nil, fmt.Errorf("%w: session belongs to an inactive source", platforms.ErrHistoryUnavailable)
	}
	return identityInfo(record), nil
}

func identityInfo(row sessioncatalog.Session) *platforms.SessionIdentity {
	return &platforms.SessionIdentity{ID: row.ID, OwnerID: row.OwnerID, Platform: row.Platform, SourceKey: row.SourceKey,
		NativeID: row.NativeID, ParentID: row.ParentID, NativeParentID: row.NativeParentID, Origin: string(row.Origin), OriginID: row.OriginID,
		CreationState: string(row.CreationState), Purged: row.Purged, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

func identityRecord(info *platforms.SessionIdentity) sessioncatalog.Session {
	return sessioncatalog.Session{ID: info.ID, OwnerID: info.OwnerID, Platform: info.Platform, SourceKey: info.SourceKey,
		NativeID: info.NativeID, ParentID: info.ParentID, NativeParentID: info.NativeParentID, Origin: sessioncatalog.Origin(info.Origin), OriginID: info.OriginID,
		CreationState: sessioncatalog.CreationState(info.CreationState), Purged: info.Purged, CreatedAt: info.CreatedAt, UpdatedAt: info.UpdatedAt}
}
