package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/statev2"
)

func (s *Server) handleSessionContentSearch(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var body struct {
		Query string `json:"query"`
	}
	if !httpx.ReadJSON(w, r, 16<<10, &body) {
		return
	}
	query := strings.TrimSpace(body.Query)
	if query == "" || len(query) > 1024 {
		http.Error(w, "search query must be between 1 and 1024 bytes", http.StatusBadRequest)
		return
	}
	if !s.hasStoredHistory() {
		http.Error(w, "conversation search unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	store := s.historyAnalytics.Store
	ready, err := store.SessionSearchIndexReady(ctx)
	if err != nil {
		httpx.ServerError(w, "read session search index progress", err)
		return
	}
	matches, err := store.SearchSessionText(ctx, s.historyOwner, query)
	if err != nil {
		httpx.ServerError(w, "search session conversation text", err)
		return
	}
	httpx.WriteJSON(w, struct {
		Matches  []statev2.SessionTextMatch `json:"matches"`
		Indexing bool                       `json:"indexing"`
	}{matches, !ready})
}
