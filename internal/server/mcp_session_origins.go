package server

import (
	"context"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

// applyCatalogueOrigins fills the creation origin that platform summaries omit,
// so MCP callers can select or exclude routine and plugin sessions. It reads
// one indexed batch from the session catalogue and leaves unknown rows as-is.
func (s *Server) applyCatalogueOrigins(ctx context.Context, sessions []db.Session) error {
	if s.stateDB == nil {
		return nil
	}
	var ids []string
	for _, session := range sessions {
		if session.Origin == "" && sessioncatalog.IsID(session.ID) {
			ids = append(ids, session.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	catalogue, err := sessioncatalog.New(s.stateDB)
	if err != nil {
		return err
	}
	rows, err := catalogue.GetMany(ctx, ids)
	if err != nil {
		return err
	}
	for i := range sessions {
		if row, ok := rows[sessions[i].ID]; ok && sessions[i].Origin == "" {
			sessions[i].Origin, sessions[i].OriginID = string(row.Origin), row.OriginID
		}
	}
	return nil
}
