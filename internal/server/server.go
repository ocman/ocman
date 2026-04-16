package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/state"
)

//go:embed static/* all:static/.gitkeep
var staticFS embed.FS

const (
	autoArchiveAfter    = 7 * 24 * time.Hour
	autoArchiveInterval = 24 * time.Hour
)

// Server serves the web UI and API.
type Server struct {
	db      *db.DB
	stateDB *state.DB
	addr    string
}

// New creates a new server.
func New(database *db.DB, stateDB *state.DB, addr string) *Server {
	return &Server{db: database, stateDB: stateDB, addr: addr}
}

// Start starts the HTTP server. It blocks until the context is cancelled,
// then gracefully shuts down the server.
func (s *Server) Start(ctx context.Context) error {
	go s.runAutoArchiveLoop(ctx)

	mux := http.NewServeMux()

	// API routes — read-only endpoints enforce GET, mutating endpoints enforce POST
	mux.HandleFunc("/api/stats", requireGET(s.handleStats))
	mux.HandleFunc("/api/projects", requireGET(s.handleProjects))
	mux.HandleFunc("/api/sessions", requireGET(s.handleSessions))
	mux.HandleFunc("/api/session/", requireGET(s.handleSession))
	mux.HandleFunc("/api/session/archive", requirePOST(s.handleArchiveSession))
	mux.HandleFunc("/api/session/seen", requirePOST(s.handleSeenSession))
	mux.HandleFunc("/api/activity", requireGET(s.handleActivity))
	mux.HandleFunc("/api/models", requireGET(s.handleModels))
	mux.HandleFunc("/api/hourly", requireGET(s.handleHourly))
	mux.HandleFunc("/api/hourly-tokens", requireGET(s.handleHourlyTokens))
	mux.HandleFunc("/api/session-port/", requireGET(s.handleSessionPort))
	mux.HandleFunc("/api/send-message", requirePOST(s.handleSendMessage))
	mux.HandleFunc("/api/respond-permission", requirePOST(s.handleRespondPermission))
	mux.HandleFunc("/api/respond-question", requirePOST(s.handleRespondQuestion))
	mux.HandleFunc("/api/reject-question", requirePOST(s.handleRejectQuestion))
	mux.HandleFunc("/api/abort-session", requirePOST(s.handleAbortSession))
	mux.HandleFunc("/api/create-session", requirePOST(s.handleCreateSession))
	mux.HandleFunc("/api/events/", s.handleEvents)
	mux.HandleFunc("/api/whisper/status", requireGET(s.handleWhisperStatus))
	mux.HandleFunc("/api/transcribe", requirePOST(s.handleTranscribe))
	mux.HandleFunc("/api/commands", requireGET(s.handleCommands))
	mux.HandleFunc("/api/command", requirePOST(s.handleExecuteCommand))
	mux.HandleFunc("/api/tmux/clients", requireGET(requireLocalhost(s.handleTmuxClients)))
	mux.HandleFunc("/api/tmux/sessions", requireGET(requireLocalhost(s.handleTmuxSessions)))
	mux.HandleFunc("/api/tmux/switch", requirePOST(requireLocalhost(s.handleTmuxSwitch)))

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

	httpServer := &http.Server{
		Addr:    s.addr,
		Handler: mux,
	}

	// Start the server in a goroutine so we can wait for the context.
	errCh := make(chan error, 1)
	go func() {
		log.WithField("addr", s.addr).Info("ocman server started")
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	// Wait for context cancellation (signal) or server error.
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutting down server...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	}
}

func (s *Server) runAutoArchiveLoop(ctx context.Context) {
	s.autoArchiveInactiveSessions()

	ticker := time.NewTicker(autoArchiveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.autoArchiveInactiveSessions()
		}
	}
}

func (s *Server) autoArchiveInactiveSessions() {
	cutoff := time.Now().Add(-autoArchiveAfter).UnixMilli()
	sessions, err := s.db.GetSessionsInactiveBefore(cutoff)
	if err != nil {
		log.WithError(err).Error("listing inactive sessions for auto-archive")
		return
	}

	archivedCount := 0
	for _, session := range sessions {
		if err := s.stateDB.ArchiveSession(session.ID, session.TimeUpdated); err != nil {
			log.WithFields(log.Fields{"sessionID": session.ID, "error": err}).Error("auto-archiving inactive session")
			continue
		}
		archivedCount++
	}

	log.WithFields(log.Fields{
		"cutoff":   cutoff,
		"archived": archivedCount,
	}).Info("auto-archive pass completed")
}

// requireGET wraps a handler to only allow GET requests.
func requireGET(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h(w, r)
	}
}

// requirePOST wraps a handler to only allow POST requests.
func requirePOST(h http.HandlerFunc) http.HandlerFunc {
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
