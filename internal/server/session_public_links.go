package server

import (
	"context"
	"net/url"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

func (s *Server) publicSessionURL(ctx context.Context, platform, nativeID string) (string, error) {
	id := nativeID
	if s.sessionCatalogue != nil {
		info, err := s.sessions.LookupSessionIdentity(ctx, platform, platforms.SessionIdentityRequest{NativeID: nativeID})
		if err != nil {
			return "", err
		}
		id, platform = info.ID, info.Platform
	}
	return s.publicURL("/session/" + url.PathEscape(id) + "?platform=" + url.QueryEscape(platform)), nil
}

// The push worker keeps native deduplication/lifecycle keys but sends canonical
// browser destinations. Resolve each owner in batches, not once per push row.
func (s *Server) notificationPublicIDs(ctx context.Context, entries []notifyEntry) (map[state.Key]string, error) {
	out := make(map[state.Key]string, len(entries))
	groups := map[string][]string{}
	for _, entry := range entries {
		if s.sessionCatalogue == nil {
			out[state.Key{Platform: entry.platform, SessionID: entry.ID}] = entry.ID
			continue
		}
		groups[entry.platform] = append(groups[entry.platform], entry.ID)
	}
	for platform, ids := range groups {
		for start := 0; start < len(ids); start += 500 {
			rows, err := s.sessions.LookupSessionIdentities(ctx, platform, ids[start:min(start+500, len(ids))])
			if err != nil {
				return nil, err
			}
			for _, row := range rows {
				out[state.Key{Platform: platform, SessionID: row.NativeID}] = row.ID
			}
		}
	}
	return out, nil
}
