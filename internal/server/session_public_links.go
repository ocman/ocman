package server

import (
	"context"
	"net/url"

	"github.com/NoUseFreak/ocman/internal/state"
)

func (s *Server) publicSessionURL(ctx context.Context, platform, reference string) (string, error) {
	id := reference
	if s.sessionCatalogue != nil {
		info, err := s.sessions.LookupSessionReference(ctx, platform, reference)
		if err != nil {
			return "", err
		}
		id, platform = info.ID, info.Platform
	}
	return s.publicURL("/session/" + url.PathEscape(id) + "?platform=" + url.QueryEscape(platform)), nil
}

// Resolve old notification references through the compatibility integration;
// canonical references need no owner RPC or native-key processing here.
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
			rows, err := s.sessions.PublicSessionReferences(ctx, platform, ids[start:min(start+500, len(ids))])
			if err != nil {
				return nil, err
			}
			for reference, id := range rows {
				out[state.Key{Platform: platform, SessionID: reference}] = id
			}
		}
	}
	return out, nil
}
