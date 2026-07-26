package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/NoUseFreak/ocman/internal/git"
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
			Workspace:     &workflowWorkspaceProvider{},
			Blobs:         workflows.NewBlobStore(blobDir),
			Notify:        s.broadcastWorkflowRunUpdated,
			NotifyTrigger: s.broadcastWorkflowTriggerUpdated,
			Forge:         &workflowForge{s: s},
			Status:        &workflowStatusInferer{s: s},
			Usage:         &workflowUsage{s: s},
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
	if rest == "validate" {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		source, ok := readWorkflowSource(w, r)
		if !ok {
			return
		}
		validated, err := s.workflowSvc().Validate(r.Context(), source)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, validated)
		return
	}
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
			source, ok := readWorkflowSource(w, r)
			if !ok {
				return
			}
			version, err := s.workflowSvc().Publish(r.Context(), source)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSONStatus(w, http.StatusCreated, version)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	versionID, action, extra := cutWorkflowPath(rest)
	if extra != "" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	switch {
	case r.Method == http.MethodDelete && action == "":
		if err := s.workflowSvc().Archive(r.Context(), versionID); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && action == "activate":
		version, err := s.workflowSvc().Activate(r.Context(), versionID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, version)
	case r.Method == http.MethodPost && action == "deactivate":
		version, err := s.workflowSvc().Deactivate(r.Context(), versionID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, version)
	case r.Method == http.MethodPost && (action == "runs" || action == "start"):
		var (
			run workflows.RunDetail
			err error
		)
		if action == "runs" {
			run, err = s.workflowSvc().Start(r.Context(), versionID)
		} else {
			run, err = s.workflowSvc().StartActive(r.Context(), versionID)
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSONStatus(w, http.StatusCreated, run)
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func readWorkflowSource(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	source, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "reading body: "+err.Error(), http.StatusBadRequest)
		return nil, false
	}
	return source, true
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
	case "retry-from":
		if extra == "" {
			http.Error(w, "node id is required", http.StatusBadRequest)
			return
		}
		var body struct {
			VersionID string `json:"versionId"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody))
		decoder.DisallowUnknownFields()
		if decodeErr := decoder.Decode(&body); decodeErr != nil {
			http.Error(w, "invalid retry request: "+decodeErr.Error(), http.StatusBadRequest)
			return
		}
		run, err = s.workflowSvc().RetryFrom(r.Context(), runID, extra, body.VersionID)
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
	artifact, payload, err := s.workflowSvc().DownloadArtifact(r.Context(), runID, artifactID)
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

// workflowWorkspaceProvider creates worktree shards for a run through the
// existing host-local git worktree service. Each shard gets a deterministic
// branch so re-provisioning after a restart reuses the same worktree. The
// first scheduler runs on the local host only (#320 AD-27).
type workflowWorkspaceProvider struct{}

func (workflowWorkspaceProvider) EnsureShard(ctx context.Context, runID, repo string, shard int) (string, error) {
	if repo == "" {
		return "", fmt.Errorf("workspace shard requires a repository directory")
	}
	root, err := git.ResolveRepoRoot(ctx, repo)
	if err != nil {
		return "", err
	}
	branch := fmt.Sprintf("ocman/wf-%s-shard-%d", runID, shard)
	res, err := git.CreateWorktree(ctx, git.CreateWorktreeRequest{
		RepoRoot:  root,
		Branch:    branch,
		NewBranch: true,
		BaseRef:   git.ResolveBaseRef(ctx, root),
	})
	if err != nil {
		return "", err
	}
	return res.Path, nil
}
