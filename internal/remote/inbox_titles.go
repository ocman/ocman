package remote

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

// Enrich the existing inbox response on its owner. The hub must not download
// a session inventory or transcript just to label an inbox item.
func (s *Server) inboxSessionTitles(ctx context.Context, items []state.InboxItem) []state.InboxItem {
	titles := map[[2]string]string{}
	for i := range items {
		var session state.InboxSession
		if items[i].Session != nil {
			session = *items[i].Session
		} else if items[i].Permission != nil {
			p := items[i].Permission
			session = state.InboxSession{Platform: p.Platform, SessionID: p.SessionID}
		} else {
			continue
		}
		key := [2]string{session.Platform, session.SessionID}
		title, loaded := titles[key]
		if !loaded && s.registry != nil {
			if adapter, ok := s.registry.Get(platforms.ID(session.Platform)); ok {
				if reader, ok := adapter.(interface {
					SessionMetadata(context.Context, string) (*db.Session, error)
				}); ok {
					if row, err := reader.SessionMetadata(ctx, session.SessionID); err == nil && row != nil {
						title = row.Title
					}
				}
			}
			titles[key] = title
		}
		if title != "" {
			session.Title = title
		}
		items[i].Session = &session
	}
	return items
}
