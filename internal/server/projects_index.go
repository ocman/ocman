package server

import (
	"context"
	"sync"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	log "github.com/sirupsen/logrus"
)

type projectsIndexState struct {
	mu          sync.RWMutex
	data        []db.ProjectStats
	loaded      bool
	refreshedAt time.Time
}

func (s *Server) runProjectsIndexLoop(ctx context.Context) {
	if s.db == nil {
		return
	}

	if err := s.refreshProjectsIndex(); err != nil {
		log.WithError(err).Warn("refreshing projects index")
	}

	ticker := time.NewTicker(projectsScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.refreshProjectsIndex(); err != nil {
				log.WithError(err).Warn("refreshing projects index")
			}
		}
	}
}

func (s *Server) refreshProjectsIndex() error {
	if s.db == nil {
		return nil
	}

	projects, err := s.db.GetProjects()
	if err != nil {
		return err
	}

	s.projects.mu.Lock()
	s.projects.data = cloneProjectStats(projects)
	s.projects.loaded = true
	s.projects.refreshedAt = time.Now()
	s.projects.mu.Unlock()

	return nil
}

func (s *Server) projectsSnapshot() ([]db.ProjectStats, bool) {
	s.projects.mu.RLock()
	defer s.projects.mu.RUnlock()
	return cloneProjectStats(s.projects.data), s.projects.loaded
}

func cloneProjectStats(projects []db.ProjectStats) []db.ProjectStats {
	if len(projects) == 0 {
		return nil
	}
	cloned := make([]db.ProjectStats, len(projects))
	copy(cloned, projects)
	return cloned
}
