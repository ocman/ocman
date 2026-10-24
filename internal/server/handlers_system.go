package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/remote"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/whisper"
)

// WithLogPath records the resolved log file path ("" = stderr only) so
// dialogs and the UI can point users at it.
func (s *Server) WithLogPath(path string) *Server {
	s.logPath = path
	return s
}

// WithVersion records the build version reported by /api/stats.
func (s *Server) WithVersion(v string) *Server {
	s.version = v
	return s
}

// LogPath returns the resolved log file path, "" when logging to stderr only.
func (s *Server) LogPath() string { return s.logPath }

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.listProjects(r.Context())
	if err != nil {
		httpx.ServerError(w, "fetching projects", err)
		return
	}
	httpx.WriteJSON(w, projects)
}

func (s *Server) listProjects(ctx context.Context) ([]db.ProjectStats, error) {
	// Local projects flow through the host seam and owner-local statev2 history.
	projects, err := s.router().Local().Projects(ctx)
	if err != nil {
		return nil, err
	}
	// Append remote projects from the inventory cache, tagged with their
	// owning remote so the frontend can label them and target the right
	// machine when creating a session.
	if s.remoteProjectsFn != nil {
		projects = append(projects, s.remoteProjectsFn()...)
	} else if s.remotes != nil {
		projects = append(projects, s.remotes.RemoteProjects()...)
	}
	// Apply archive state after remotes are appended so remote projects
	// honour the archived_project markers too (they otherwise re-appear on
	// every refresh). Newer activity still auto-unarchives — most recent
	// timestamp wins.
	if err := s.applyProjectArchiveState(ctx, projects); err != nil {
		return nil, err
	}
	// Fold worktree directories into their repo root and merge duplicate
	// rows so each project appears once, then sort by last activity.
	projects = foldWorktreeProjects(projects)
	remote.AssignProjectKeys(projects)
	if err := s.applyProjectMerges(ctx, projects); err != nil {
		return nil, err
	}
	sort.SliceStable(projects, func(i, j int) bool {
		return projects[i].LastUsed > projects[j].LastUsed
	})
	if projects == nil {
		projects = []db.ProjectStats{} // [] not null for the frontend
	}
	return projects, nil
}

// foldWorktreeProjects collapses <repo>/.worktrees/<repo>/<slug> directories
// back to the repo root and merges rows that fold to the same project,
// summing aggregate stats and keeping the newest activity. Rows are keyed
// per owning host (RemoteID) so identical paths on different machines stay
// separate. The folded root replaces the directory of the merged entry.
func foldWorktreeProjects(projects []db.ProjectStats) []db.ProjectStats {
	type key struct{ remoteID, root string }
	merged := make(map[key]*db.ProjectStats)
	order := make([]key, 0, len(projects))
	for _, p := range projects {
		k := key{p.RemoteID, projectRootForDirectory(p.Directory)}
		agg, ok := merged[k]
		if !ok {
			cp := p
			cp.Directory = k.root
			merged[k] = &cp
			order = append(order, k)
			continue
		}
		agg.SessionCount += p.SessionCount
		agg.UpstreamKeys = append(agg.UpstreamKeys, p.UpstreamKeys...)
		agg.MessageCount += p.MessageCount
		agg.TotalTokensIn += p.TotalTokensIn
		agg.TotalTokensOut += p.TotalTokensOut
		agg.TotalCost += p.TotalCost
		if p.LastUsed > agg.LastUsed {
			agg.LastUsed = p.LastUsed
		}
		agg.Archived = agg.Archived && p.Archived
	}
	out := make([]db.ProjectStats, 0, len(order))
	for _, k := range order {
		out = append(out, *merged[k])
	}
	return out
}

// applyProjectArchiveState overlays the Archived flag from state.db onto
// a project slice, folding directories to their project root. A project
// auto-unarchives (and the marker is deleted) when any of its sessions'
// activity (LastUsed) is newer than archived_at, mirroring the per-
// session archive semantics in applySessionState.
//
// Everything is keyed per owning host (remoteID, root), matching
// foldWorktreeProjects: the same path on two machines is two projects, so
// one host's archive must not hide the other's and activity on one must not
// unarchive the other.
func (s *Server) applyProjectArchiveState(ctx context.Context, projects []db.ProjectStats) error {
	archived, err := s.stateDB.ArchivedProjects(ctx)
	if err != nil {
		return err
	}
	if len(archived) == 0 {
		return nil
	}

	// Newest activity per (host, folded root) across all matching directories.
	newest := map[state.ProjectKey]int64{}
	for _, p := range projects {
		key := projectArchiveKey(p.RemoteID, p.Directory)
		if p.LastUsed > newest[key] {
			newest[key] = p.LastUsed
		}
	}

	// Auto-unarchive any project with newer activity than its archive
	// time, persisting the change so it stays unarchived.
	for key, archivedAt := range archived {
		if newest[key] > archivedAt {
			if err := s.stateDB.UnarchiveProject(ctx, key.RemoteID, key.Root); err != nil {
				return err
			}
			delete(archived, key)
		}
	}

	for i := range projects {
		if _, ok := archived[projectArchiveKey(projects[i].RemoteID, projects[i].Directory)]; ok {
			projects[i].Archived = true
		}
	}
	return nil
}

// projectArchiveKey builds a project's archive identity: its owning host
// plus its folded repo root. An unstamped RemoteID means the local machine.
func projectArchiveKey(remoteID, directory string) state.ProjectKey {
	return state.ProjectKey{
		RemoteID: state.NormalizeRemoteID(remoteID),
		Root:     projectRootForDirectory(directory),
	}
}

func (s *Server) handleWhisperStatus(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, map[string]interface{}{
		"available": whisper.Available(),
	})
}

func (s *Server) handleTranscribe(w http.ResponseWriter, r *http.Request) {
	if !whisper.Available() {
		http.Error(w, "whisper is not available", http.StatusServiceUnavailable)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxAudioUpload)
	httpx.SetBodyReadDeadline(w, uploadReadTimeout)

	file, header, err := r.FormFile("audio")
	if err != nil {
		log.WithError(err).Warn("failed to read audio upload")
		http.Error(w, "failed to read audio file", http.StatusBadRequest)
		return
	}
	defer file.Close()
	// Body is in hand; transcription itself can take minutes.
	httpx.ClearBodyReadDeadline(w)

	ext := filepath.Ext(header.Filename)
	if ext == "" {
		ext = ".wav"
	}
	tmp, err := os.CreateTemp("", "ocman-audio-*"+ext)
	if err != nil {
		httpx.ServerError(w, "creating temp file", err)
		return
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	if _, err := io.Copy(tmp, file); err != nil {
		httpx.ServerError(w, "writing audio to temp file", err)
		return
	}
	tmp.Close()

	text, err := whisper.TranscribeFile(tmp.Name())
	if err != nil {
		log.WithError(err).Error("transcription failed")
		http.Error(w, "transcription failed", http.StatusInternalServerError)
		return
	}

	httpx.WriteJSON(w, map[string]interface{}{"text": text})
}

func (s *Server) handleDebugLog(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Level   string          `json:"level"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
		return
	}

	fields := log.Fields{"source": "fe"}
	if ua := r.Header.Get("User-Agent"); ua != "" {
		fields["ua"] = ua
	}
	if len(req.Data) > 0 {
		fields["data"] = string(req.Data)
	}

	entry := log.WithFields(fields)
	switch strings.ToLower(strings.TrimSpace(req.Level)) {
	case "error":
		entry.Error(req.Message)
	case "warn", "warning":
		entry.Warn(req.Message)
	case "debug":
		entry.Debug(req.Message)
	default:
		entry.Info(req.Message)
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleSystemStats returns backend runtime statistics (memory usage, uptime, etc).
//
// The `db` block is included only when ocman has an OpenCode read-only
// handle (i.e. when the opencode platform adapter is registered). It
// surfaces database/sql's connection-pool stats so we can watch for
// the failure modes documented in docs/other/profiling.md.
func (s *Server) handleSystemStats(w http.ResponseWriter, r *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	stats := map[string]interface{}{
		"memory": map[string]interface{}{
			"alloc":        m.Alloc,
			"totalAlloc":   m.TotalAlloc,
			"sys":          m.Sys,
			"heapAlloc":    m.HeapAlloc,
			"heapSys":      m.HeapSys,
			"heapInuse":    m.HeapInuse,
			"heapIdle":     m.HeapIdle,
			"heapReleased": m.HeapReleased,
		},
		"gc": map[string]interface{}{
			"numGC":   m.NumGC,
			"lastGC":  m.LastGC,
			"pauseNs": m.PauseNs[(m.NumGC+255)%256],
		},
		"goroutines": runtime.NumGoroutine(),
		"uptime":     time.Since(s.startTime).Seconds(),
	}

	if s.db != nil {
		ds := s.db.Stats()
		stats["db"] = map[string]interface{}{
			"max_open_conns":   ds.MaxOpenConnections,
			"open_conns":       ds.OpenConnections,
			"in_use":           ds.InUse,
			"idle":             ds.Idle,
			"wait_count":       ds.WaitCount,
			"wait_duration_ms": ds.WaitDuration.Milliseconds(),
		}
	}

	httpx.WriteJSON(w, stats)
}
