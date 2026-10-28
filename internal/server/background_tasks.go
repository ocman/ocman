package server

import (
	"github.com/ocman/ocman/internal/taskmod"
	"github.com/ocman/ocman/internal/tasksvc"
)

func (s *Server) taskManager() *tasksvc.Manager {
	return s.tasks.get(func() *tasksvc.Manager { return tasksvc.New(s.stateDB, 4) })
}

func (s *Server) backgroundTasksModule() *taskmod.Module {
	return taskmod.New(s.taskManager())
}
