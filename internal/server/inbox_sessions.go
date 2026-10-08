package server

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/remote"
	"github.com/NoUseFreak/ocman/internal/state"
)

func (s *Server) inboxSessionContext(ctx context.Context, source string, items []state.InboxItem) []state.InboxItem {
	items = append([]state.InboxItem(nil), items...)
	titles := map[[2]string]string{}
	for i := range items {
		item := &items[i]
		var session state.InboxSession
		if item.Session != nil {
			session = *item.Session
		} else if item.Permission != nil {
			session = state.InboxSession{Platform: item.Permission.Platform, SessionID: item.Permission.SessionID}
		} else {
			continue
		}
		if source != "local" {
			// The owner enriched this row in the existing InboxItems RPC.
			session.Platform = remote.CompoundPlatformID(source, session.Platform)
		} else if s.db != nil {
			key := [2]string{session.Platform, session.SessionID}
			title, loaded := titles[key]
			if !loaded {
				if row, err := s.db.GetSession(ctx, session.SessionID); err == nil {
					title = row.Title
				}
				titles[key] = title
			}
			if title != "" {
				session.Title = title
			}
		}
		item.Session = &session
		if item.Permission != nil {
			label := session.Title
			if label == "" {
				label = session.SessionID
			}
			item.Title = label + ": Permission requested: " + item.Permission.Permission
		}
	}
	return items
}
