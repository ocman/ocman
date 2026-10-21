package server

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func (s *Server) hasStoredHistory() bool {
	return s != nil && s.historyAnalytics != nil && s.historyAnalytics.Store != nil
}

func (s *Server) projectHistoryVersion(ctx context.Context) (int64, error) {
	if s.projects.fetch != nil || !s.hasStoredHistory() {
		return 0, nil
	}
	return s.historyAnalytics.Store.ProjectVersion(ctx, s.historyOwner)
}

func (s *Server) storedProjects(ctx context.Context) ([]db.ProjectStats, error) {
	if s.historyAnalytics == nil || s.historyAnalytics.Store == nil {
		return nil, platforms.ErrHistoryUnavailable
	}
	// Browse committed projects while ingestion catches up. Only complete-history
	// analytics require every source to be ready; no native fallback is used here.
	rows, err := s.historyAnalytics.Store.Projects(ctx, s.historyOwner)
	if err != nil {
		return nil, err
	}
	out := make([]db.ProjectStats, 0, len(rows))
	for _, row := range rows {
		out = append(out, db.ProjectStats{Directory: row.Directory, SessionCount: row.SessionCount, MessageCount: row.MessageCount, LastUsed: row.LastUsed, TotalTokensIn: row.TotalTokensIn, TotalTokensOut: row.TotalTokensOut, TotalCost: row.TotalCost})
	}
	return out, nil
}

func (s *Server) storedDirectories(ctx context.Context) []string {
	if s.historyAnalytics == nil || s.historyAnalytics.Store == nil {
		return nil
	}
	directories, err := s.historyAnalytics.Store.Directories(ctx, s.historyOwner)
	if err != nil {
		return nil
	}
	return directories
}
