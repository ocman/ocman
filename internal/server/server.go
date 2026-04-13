package server

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
)

//go:embed static/*
var staticFS embed.FS

// Server serves the web UI and API.
type Server struct {
	db   *db.DB
	addr string
}

// New creates a new server.
func New(database *db.DB, addr string) *Server {
	return &Server{db: database, addr: addr}
}

// Start starts the HTTP server.
func (s *Server) Start() error {
	mux := http.NewServeMux()

	// API routes — read-only endpoints enforce GET, mutating endpoints enforce POST
	mux.HandleFunc("/api/stats", s.requireGET(s.handleStats))
	mux.HandleFunc("/api/projects", s.requireGET(s.handleProjects))
	mux.HandleFunc("/api/sessions", s.requireGET(s.handleSessions))
	mux.HandleFunc("/api/session/", s.requireGET(s.handleSession))
	mux.HandleFunc("/api/activity", s.requireGET(s.handleActivity))
	mux.HandleFunc("/api/models", s.requireGET(s.handleModels))
	mux.HandleFunc("/api/hourly", s.requireGET(s.handleHourly))
	mux.HandleFunc("/api/session-port/", s.requireGET(s.handleSessionPort))
	mux.HandleFunc("/api/send-message", s.requirePOST(s.handleSendMessage))
	mux.HandleFunc("/api/create-session", s.requirePOST(s.handleCreateSession))
	mux.HandleFunc("/api/events/", s.handleEvents)

	// Static files with SPA fallback
	staticContent, err := fs.Sub(staticFS, "static")
	if err != nil {
		return fmt.Errorf("failed to get static subtree: %w", err)
	}
	fileServer := http.FileServer(http.FS(staticContent))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Try to serve the file directly
		path := r.URL.Path
		if path == "/" {
			fileServer.ServeHTTP(w, r)
			return
		}
		// Check if the file exists in static
		f, err := staticContent.Open(strings.TrimPrefix(path, "/"))
		if err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		// SPA fallback: serve index.html for client-side routes
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})

	log.WithField("addr", s.addr).Info("ocman server started")
	return http.ListenAndServe(s.addr, mux)
}

// requireGET wraps a handler to only allow GET requests.
func (s *Server) requireGET(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h(w, r)
	}
}

// requirePOST wraps a handler to only allow POST requests.
func (s *Server) requirePOST(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h(w, r)
	}
}

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.WithError(err).Error("failed to encode JSON response")
	}
}

// serverError logs the real error and returns a generic message to the client.
func serverError(w http.ResponseWriter, msg string, err error) {
	log.WithError(err).Error(msg)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}
