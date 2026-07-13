package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/NoUseFreak/ocman/internal/workflows"
)

func (s *Server) workflowSvc() *workflows.Service {
	s.workflowSvcOnce.Do(func() {
		s.workflowSvcCached = workflows.NewService(workflows.Deps{
			Store:         s.stateDB,
			Agent:         &workflowAgentExecutor{s: s},
			Notify:        s.broadcastWorkflowRunUpdated,
			NotifyTrigger: s.broadcastWorkflowTriggerUpdated,
			Forge:         &loopForge{s: s},
			Status:        &loopStatusInferer{s: s},
		})
	})
	return s.workflowSvcCached
}

func (s *Server) handleWorkflows(w http.ResponseWriter, r *http.Request) {
	if s.stateDB == nil {
		http.Error(w, "state database unavailable", http.StatusServiceUnavailable)
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/workflows"), "/")
	if rest == "" {
		switch r.Method {
		case http.MethodGet:
			versions, err := s.workflowSvc().ListVersions(r.Context())
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, versions)
		case http.MethodPost:
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
			source, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "reading body: "+err.Error(), http.StatusBadRequest)
				return
			}
			version, err := s.workflowSvc().PublishJSON(r.Context(), source)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusCreated)
			writeJSON(w, version)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	versionID, action, extra := cutWorkflowPath(rest)
	if r.Method == http.MethodPost && versionID == "validate" && action == "" {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
		source, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "reading body: "+err.Error(), http.StatusBadRequest)
			return
		}
		definition, err := s.workflowSvc().ValidateJSON(r.Context(), source)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, definition)
		return
	}
	if r.Method != http.MethodPost || action != "runs" || extra != "" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	run, err := s.workflowSvc().Start(r.Context(), versionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, run)
}

func (s *Server) handleWorkflowRuns(w http.ResponseWriter, r *http.Request) {
	if s.stateDB == nil {
		http.Error(w, "state database unavailable", http.StatusServiceUnavailable)
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/workflow-runs"), "/")
	if rest == "" {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		runs, err := s.workflowSvc().ListRuns(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, runs)
		return
	}
	runID, action, extra := cutWorkflowPath(rest)
	if action == "" && r.Method == http.MethodGet {
		run, err := s.workflowSvc().GetRun(r.Context(), runID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		writeJSON(w, run)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var (
		run workflows.RunDetail
		err error
	)
	switch action {
	case "approve":
		if extra == "" {
			http.Error(w, "node id is required", http.StatusBadRequest)
			return
		}
		run, err = s.workflowSvc().Approve(r.Context(), runID, extra)
	case "pause":
		run, err = s.workflowSvc().Pause(r.Context(), runID)
	case "resume":
		run, err = s.workflowSvc().Resume(r.Context(), runID)
	case "cancel":
		run, err = s.workflowSvc().Cancel(r.Context(), runID)
	case "resolve-unknown":
		attemptID, parseErr := strconv.ParseInt(extra, 10, 64)
		if parseErr != nil || attemptID <= 0 {
			http.Error(w, "positive attempt id is required", http.StatusBadRequest)
			return
		}
		var body struct {
			Resolution string `json:"resolution"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody))
		decoder.DisallowUnknownFields()
		if decodeErr := decoder.Decode(&body); decodeErr != nil {
			http.Error(w, "invalid resolution: "+decodeErr.Error(), http.StatusBadRequest)
			return
		}
		run, err = s.workflowSvc().ResolveUnknown(r.Context(), runID, attemptID, body.Resolution)
	default:
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, run)
}

func cutWorkflowPath(path string) (first, second, rest string) {
	first, path, _ = strings.Cut(path, "/")
	second, rest, _ = strings.Cut(path, "/")
	return first, second, rest
}
