package server

import (
	"net/http"

	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/queuemod"
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
	serveSessionRoute(s.queueModule().List, w, r)
}
func (s *Server) handleSessionQueueMove(w http.ResponseWriter, r *http.Request) {
	serveSessionRoute(s.queueModule().Move, w, r)
}
func (s *Server) handleSessionQueueDelete(w http.ResponseWriter, r *http.Request) {
	serveSessionRoute(s.queueModule().Delete, w, r)
}
func (s *Server) handleModelFallthroughSettings(w http.ResponseWriter, r *http.Request) {
	serveSetting(s.queueModule().Settings()).ServeHTTP(w, r)
}

func serveSetting[T any](route httpx.SettingRoute[T]) http.Handler {
	mux := http.NewServeMux()
	route.Mount(func(p string, h http.HandlerFunc) { mux.HandleFunc(p, h) }, "/")
	return mux
}

// serveSessionRoute calls h with the session path values the mux would set.
func serveSessionRoute(h http.HandlerFunc, w http.ResponseWriter, r *http.Request) {
	mux := http.NewServeMux()
	for _, p := range []string{"/api/session/{id}", "/api/session/{id}/", "/api/session/{id}/queue/{qmid}", "/api/session/{id}/queue/{qmid}/move"} {
		mux.HandleFunc(p, h)
	}
	mux.ServeHTTP(w, r)
}
