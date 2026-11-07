package server

import (
	"context"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/state"
)

func (s *Server) enrichSidebarCreators(ctx context.Context, rows []db.Session) error {
	if s.stateDB == nil || len(rows) == 0 {
		return nil
	}
	keys := make([]state.Key, len(rows))
	for i, row := range rows {
		keys[i] = state.Key{Platform: row.Platform, SessionID: row.ID}
	}
	creators, err := s.stateDB.SessionCreators(ctx, keys)
	if err != nil {
		return err
	}
	for i := range rows {
		if creator, ok := creators[keys[i]]; ok {
			rows[i].Creator = &db.SessionCreator{
				Platform: creator.CreatorPlatform, SessionID: creator.CreatorSessionID, CreatedAt: creator.CreatedAt,
			}
		}
	}
	return nil
}
