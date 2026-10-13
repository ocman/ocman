package server

import (
	"github.com/NoUseFreak/ocman/internal/queuemod"
	"net/http"
)

// These helpers keep the HTTP/SSE acceptance fixtures exercising the module.
type queuedMessageView = queuemod.MessageView

const modelFallthroughSettingKey = "model_fallthrough"
const continuationPrompt = "The previous attempt at this task was interrupted before it finished. " +
	"Files it already changed are still on disk, but its work is not in your context. " +
	"Before redoing any work, inspect the current state of the working tree " +
	"(for example `git status` and `git diff`), then complete the task."

func (s *Server) queueFlushWorker() *queuemod.Module { return s.queueModule() }
func (s *Server) handleSessionQueueList(w http.ResponseWriter, r *http.Request) {
	s.queueModule().List(w, r)
}
func (s *Server) handleSessionQueueMove(w http.ResponseWriter, r *http.Request) {
	s.queueModule().Move(w, r)
}
func (s *Server) handleSessionQueueDelete(w http.ResponseWriter, r *http.Request) {
	s.queueModule().Delete(w, r)
}
func (s *Server) handleModelFallthroughSettings(w http.ResponseWriter, r *http.Request) {
	s.queueModule().Settings(w, r)
}
