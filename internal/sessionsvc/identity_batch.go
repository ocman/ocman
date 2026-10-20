package sessionsvc

import (
	"context"
	"errors"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
)

// LookupSessionIdentities batches public projection by owner. It never turns
// a remote list into one network request per session. Unfinished identities
// are omitted so an uncertain creation cannot block unrelated list entries.
func (s *Service) LookupSessionIdentities(ctx context.Context, platformID string, nativeIDs []string) ([]platforms.SessionIdentity, error) {
	if len(nativeIDs) < 1 || len(nativeIDs) > 500 {
		return nil, validation("identity batch requires 1..500 native IDs")
	}
	if s.identities == nil || s.identities.store == nil {
		return nil, platforms.ErrHistoryUnavailable
	}
	if platformID == "" {
		return s.lookupNativeIdentities(ctx, "", nativeIDs)
	}
	p, err := s.pickAdapter(ctx, platformID)
	if err != nil {
		return nil, platforms.ErrPlatformUnreachable
	}
	if owner, base := platforms.SplitPlatformID(string(p.ID())); owner != "" {
		resolver, ok := p.(platforms.SessionIdentityResolver)
		if !ok {
			return nil, platforms.ErrUnsupported
		}
		rows, err := resolver.SessionIdentities(ctx, nativeIDs)
		if err != nil {
			return nil, err
		}
		wanted := make(map[string]bool, len(nativeIDs))
		for _, id := range nativeIDs {
			wanted[id] = true
		}
		for i := range rows {
			row := &rows[i]
			if row.OwnerID != owner || row.OwnerID != resolver.SessionOwnerID() || row.Platform != base || !wanted[row.NativeID] {
				return nil, sessioncatalog.ErrBindingConflict
			}
			delete(wanted, row.NativeID)
			if err := s.identities.store.Mirror(ctx, identityRecord(row)); err != nil {
				return nil, err
			}
			row.Platform = string(p.ID())
		}
		return rows, nil
	}
	return s.lookupNativeIdentities(ctx, string(p.ID()), nativeIDs)
}

func (s *Service) lookupNativeIdentities(ctx context.Context, platformID string, nativeIDs []string) ([]platforms.SessionIdentity, error) {
	seen := make(map[string]bool, len(nativeIDs))
	rows := make([]platforms.SessionIdentity, 0, len(nativeIDs))
	for _, id := range nativeIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		info, err := s.LookupSessionIdentity(ctx, platformID, platforms.SessionIdentityRequest{NativeID: id})
		if errors.Is(err, sessioncatalog.ErrCreating) {
			continue
		}
		if err != nil {
			return nil, err
		}
		rows = append(rows, *info)
	}
	return rows, nil
}
