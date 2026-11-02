package server

import (
	"encoding/json"
	"net/http"
	"runtime"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/ocman/ocman/internal/httpx"
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
