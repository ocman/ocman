package server

import (
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
	"github.com/NoUseFreak/ocman/internal/pricing"
	"github.com/NoUseFreak/ocman/internal/whisper"
)

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	stats, err := s.db.GetStats()
	if err != nil {
		serverError(w, "fetching stats", err)
		return
	}
	writeJSON(w, stats)
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	agent := strings.TrimSpace(r.URL.Query().Get("agent"))
	model := strings.TrimSpace(r.URL.Query().Get("model"))
	dayCount := parseIntParam(r, "days", 0)
	var since int64
	if dayCount > 0 {
		since = time.Now().Add(-time.Duration(dayCount) * 24 * time.Hour).UnixMilli()
	}
	limit := parseIntParam(r, "limit", 20)
	offset := parseIntParam(r, "offset", 0)
	sessionLimit := parseIntParam(r, "sessionLimit", 20)
	sessionOffset := parseIntParam(r, "sessionOffset", 0)
	projectLimit := parseIntParam(r, "projectLimit", 20)
	projectOffset := parseIntParam(r, "projectOffset", 0)
	dir := normaliseDirParam(r.URL.Query().Get("dir"))

	metrics, err := s.db.GetMetricsDashboard(db.MetricsDashboardOptions{
		AgentFilter:   agent,
		ModelFilter:   model,
		Since:         since,
		Days:          dayCount,
		RequestLimit:  limit,
		RequestOffset: offset,
		SessionLimit:  sessionLimit,
		SessionOffset: sessionOffset,
		ProjectLimit:  projectLimit,
		ProjectOffset: projectOffset,
		Pricing:       pricing.Load(),
		Dir:           dir,
	})
	if err != nil {
		serverError(w, "fetching metrics", err)
		return
	}
	writeJSON(w, metrics)
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	// Local hub projects flow through the host seam (AD-16).
	projects, err := s.router().Local().Projects(r.Context())
	if err != nil {
		serverError(w, "fetching projects", err)
		return
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
	if err := s.applyProjectArchiveState(projects); err != nil {
		serverError(w, "applying project archive state", err)
		return
	}
	// Fold worktree directories into their repo root and merge duplicate
	// rows so each project appears once, then sort by last activity.
	projects = foldWorktreeProjects(projects)
	sort.SliceStable(projects, func(i, j int) bool {
		return projects[i].LastUsed > projects[j].LastUsed
	})
	writeJSON(w, projects)
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
func (s *Server) applyProjectArchiveState(projects []db.ProjectStats) error {
	archived, err := s.stateDB.ArchivedProjects()
	if err != nil {
		return err
	}
	if len(archived) == 0 {
		return nil
	}

	// Newest activity per folded root across all matching directories.
	newest := map[string]int64{}
	for _, p := range projects {
		root := projectRootForDirectory(p.Directory)
		if p.LastUsed > newest[root] {
			newest[root] = p.LastUsed
		}
	}

	// Auto-unarchive any root with newer activity than its archive
	// time, persisting the change so it stays unarchived.
	for root, archivedAt := range archived {
		if newest[root] > archivedAt {
			if err := s.stateDB.UnarchiveProject(root); err != nil {
				return err
			}
			delete(archived, root)
		}
	}

	for i := range projects {
		if _, ok := archived[projectRootForDirectory(projects[i].Directory)]; ok {
			projects[i].Archived = true
		}
	}
	return nil
}

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	since := parseSinceParam(r)
	model := strings.TrimSpace(r.URL.Query().Get("model"))
	dir := normaliseDirParam(r.URL.Query().Get("dir"))
	activity, err := s.db.GetDailyActivity(since, model, dir)
	if err != nil {
		serverError(w, "fetching activity", err)
		return
	}
	writeJSON(w, activity)
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	since := parseSinceParam(r)
	dir := normaliseDirParam(r.URL.Query().Get("dir"))
	models, err := s.db.GetModelUsage(since, dir)
	if err != nil {
		serverError(w, "fetching model usage", err)
		return
	}
	writeJSON(w, models)
}

func (s *Server) handleHourlyTokens(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	since := parseSinceParam(r)
	model := strings.TrimSpace(r.URL.Query().Get("model"))
	dir := normaliseDirParam(r.URL.Query().Get("dir"))
	dayCount := parseIntParam(r, "days", 0)
	data, err := s.db.GetHourlyTokensByModel(dayCount, since, model, dir)
	if err != nil {
		serverError(w, "fetching hourly tokens by model", err)
		return
	}
	writeJSON(w, data)
}

func (s *Server) handleHourly(w http.ResponseWriter, r *http.Request) {
	if !s.requireDB(w) {
		return
	}
	since := parseSinceParam(r)
	dir := normaliseDirParam(r.URL.Query().Get("dir"))
	hourly, err := s.db.GetHourlyActivity(since, dir)
	if err != nil {
		serverError(w, "fetching hourly activity", err)
		return
	}
	writeJSON(w, hourly)
}

func (s *Server) handleWhisperStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]interface{}{
		"available": whisper.Available(),
	})
}

func (s *Server) handleTranscribe(w http.ResponseWriter, r *http.Request) {
	if !whisper.Available() {
		http.Error(w, "whisper is not available", http.StatusServiceUnavailable)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxAudioUpload)

	file, header, err := r.FormFile("audio")
	if err != nil {
		log.WithError(err).Warn("failed to read audio upload")
		http.Error(w, "failed to read audio file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	ext := filepath.Ext(header.Filename)
	if ext == "" {
		ext = ".wav"
	}
	tmp, err := os.CreateTemp("", "ocman-audio-*"+ext)
	if err != nil {
		serverError(w, "creating temp file", err)
		return
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	if _, err := io.Copy(tmp, file); err != nil {
		serverError(w, "writing audio to temp file", err)
		return
	}
	tmp.Close()

	text, err := whisper.TranscribeFile(tmp.Name())
	if err != nil {
		log.WithError(err).Error("transcription failed")
		http.Error(w, "transcription failed", http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]interface{}{"text": text})
}

func (s *Server) handleCalcCost(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ModelID    string `json:"modelID"`
		Input      int64  `json:"input"`
		Output     int64  `json:"output"`
		CacheRead  int64  `json:"cacheRead"`
		CacheWrite int64  `json:"cacheWrite"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}

	table := pricing.Load()
	cost := table.CalcCost(req.ModelID, req.Input, req.Output, req.CacheRead, req.CacheWrite)
	price := table.Lookup(req.ModelID)
	known := price.InputPerToken != 0 || price.OutputPerToken != 0

	writeJSON(w, map[string]interface{}{
		"cost":  cost,
		"known": known,
	})
}

func (s *Server) handleDebugLog(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Level   string          `json:"level"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
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

	writeJSON(w, stats)
}
