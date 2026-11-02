package server

import (
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/taskmod"
	"github.com/ocman/ocman/internal/tasksvc"
)

func (s *Server) taskManager() *tasksvc.Manager {
	return s.tasks.get(func() *tasksvc.Manager { return tasksvc.New(s.stateDB, 4) })
}

func (s *Server) backgroundTasksModule() *taskmod.Module {
	return taskmod.New(s.taskManager()).UseOwners(func() map[string]tasksvc.Reader {
		readers := map[string]tasksvc.Reader{}
		for id, host := range s.router().Remotes() {
			if tasks, ok := host.(hostsvc.BackgroundTaskHost); ok {
				readers[id] = tasks.BackgroundTaskReader()
			}
		}
		return readers
	})
}

// BackgroundTaskReader supplies only owner-local observations to remote RPCs.
func (s *Server) BackgroundTaskReader() tasksvc.Reader { return s.taskManager() }
