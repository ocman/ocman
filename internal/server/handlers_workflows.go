package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/workflows"
)

func (s *Server) workflowSvc() *workflows.Service {
	s.workflowSvcOnce.Do(func() {
		blobDir := s.workflowBlobDir
		if blobDir == "" {
			blobDir = filepath.Join(state.DefaultDataDir(), "workflow-artifacts")
		}
		s.workflowSvcCached = workflows.NewService(workflows.Deps{
			Store:         s.stateDB,
			Agent:         &workflowAgentExecutor{s: s},
			Blobs:         workflows.NewBlobStore(blobDir),
			Notify:        s.broadcastWorkflowRunUpdated,
			NotifyTrigger: s.broadcastWorkflowTriggerUpdated,
			Forge:         &loopForge{s: s},
			Status:        &loopStatusInferer{s: s},
			Usage:         &loopUsage{s: s},
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
	if action == "artifacts" && r.Method == http.MethodGet {
		s.handleWorkflowArtifacts(w, r, runID, extra)
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

// handleWorkflowArtifacts serves artifact metadata for a run and the
// content-addressed payload for a single artifact.
//
//	GET /api/workflow-runs/{run}/artifacts            → metadata list
//	GET /api/workflow-runs/{run}/artifacts/{id}/download → payload bytes
func (s *Server) handleWorkflowArtifacts(w http.ResponseWriter, r *http.Request, runID, extra string) {
	if extra == "" {
		artifacts, err := s.workflowSvc().ListArtifacts(r.Context(), runID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, artifacts)
		return
	}
	artifactID, sub, _ := strings.Cut(extra, "/")
	if sub != "download" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	artifact, payload, err := s.workflowSvc().DownloadArtifact(r.Context(), artifactID)
	if errors.Is(err, workflows.ErrPayloadMissing) {
		http.Error(w, "artifact payload has been cleaned up", http.StatusGone)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Artifact-Kind", artifact.Kind)
	w.Header().Set("Content-Disposition", "attachment; filename=\""+artifactDownloadName(artifact)+"\"")
	_, _ = w.Write(payload)
}

// artifactDownloadName derives a stable, safe download filename from the
// artifact's name and kind.
func artifactDownloadName(a workflows.Artifact) string {
	name := a.Name
	if name == "" {
		name = a.ID
	}
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		default:
			return '_'
		}
	}, name)
	switch a.Kind {
	case workflows.KindJSON:
		if !strings.HasSuffix(name, ".json") {
			name += ".json"
		}
	case workflows.KindDiff:
		if !strings.HasSuffix(name, ".diff") && !strings.HasSuffix(name, ".patch") {
			name += ".diff"
		}
	}
	return name
}

func cutWorkflowPath(path string) (first, second, rest string) {
	first, path, _ = strings.Cut(path, "/")
	second, rest, _ = strings.Cut(path, "/")
	return first, second, rest
}
