package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/NoUseFreak/ocman/internal/workflows"
)

func (s *Server) handleDaguStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.router().ForRemote(r.URL.Query().Get("remoteId")).DaguStatus(r.Context()))
}

func (s *Server) handleDaguRuns(w http.ResponseWriter, r *http.Request) {
	action := strings.TrimPrefix(r.URL.Path, "/api/dagu/runs/")
	remoteID := r.URL.Query().Get("remoteId")
	var name, runID string
	var definition workflows.Definition
	if r.Method == http.MethodPost {
		var request struct {
			RemoteID   string               `json:"remoteId"`
			Name       string               `json:"name"`
			RunID      string               `json:"runId"`
			Definition workflows.Definition `json:"definition"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		remoteID, name, runID, definition = request.RemoteID, request.Name, request.RunID, request.Definition
	} else {
		name, runID = r.URL.Query().Get("name"), r.URL.Query().Get("runId")
	}
	host := s.router().ForRemote(remoteID)
	if remoteID != "" && remoteID != "local" && host.RemoteID() != remoteID {
		http.Error(w, "host not found", http.StatusNotFound)
		return
	}
	switch action {
	case "start":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		run, err := host.StartDaguWorkflow(r.Context(), definition)
		writeDaguResult(w, run, err)
	case "get":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if name == "" || runID == "" {
			http.Error(w, "name and runId are required", http.StatusBadRequest)
			return
		}
		run, err := host.GetDaguRun(r.Context(), name, runID)
		writeDaguResult(w, run, err)
	case "cancel":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if name == "" || runID == "" {
			http.Error(w, "name and runId are required", http.StatusBadRequest)
			return
		}
		writeDaguResult(w, map[string]bool{"ok": true}, host.CancelDaguRun(r.Context(), name, runID))
	default:
		http.NotFound(w, r)
	}
}

func writeDaguResult(w http.ResponseWriter, value any, err error) {
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, value)
}
