package analytics

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
)

type analyticsSourceKey struct{}

// projectionReaders owns only independent read-only handles, never statev2's
// writer. Handles remain alive until listeners and in-flight requests stop.
type projectionReaders struct {
	mu      sync.Mutex
	readers map[string]*db.DB
}

// projection returns a reader bound to one validated read snapshot; release
// it once the response is built.
func (h *HistoryAnalytics) projection(ctx context.Context) (*db.DB, func(), string) {
	if h == nil || h.Store == nil || h.Instances == nil {
		return nil, nil, "history-unavailable"
	}
	ids, ready := h.Instances()
	if !ready || len(ids) == 0 {
		return nil, nil, "import-incomplete"
	}
	ids = append([]string(nil), ids...)
	sort.Strings(ids)
	raw, _ := json.Marshal(ids)
	key := string(raw)
	h.projections.mu.Lock()
	if h.projections.readers == nil {
		h.projections.readers = map[string]*db.DB{}
	}
	reader := h.projections.readers[key]
	if reader == nil {
		var err error
		reader, err = db.OpenHistoryAnalytics(h.Store.FilePath(), ids)
		if err != nil {
			h.projections.mu.Unlock()
			return nil, nil, "history-error"
		}
		h.projections.readers[key] = reader
	}
	h.projections.mu.Unlock()
	snapshot, release, err := reader.ScopedHistoryReader(ctx)
	if err != nil {
		return nil, nil, "history-scope-error"
	}
	return snapshot, release, ""
}

func (m *Module) analyticsReader(ctx context.Context) *db.DB {
	if reader, ok := ctx.Value(analyticsSourceKey{}).(*db.DB); ok {
		return reader
	}
	return nil
}

func (m *Module) runtimeAnalyticsReader(ctx context.Context) *db.DB {
	if reader, ok := ctx.Value(analyticsSourceKey{}).(*db.DB); ok {
		return reader
	}
	return nil
}

func (m *Module) requireRuntimeAnalytics(w http.ResponseWriter, r *http.Request) bool {
	if m.runtimeAnalyticsReader(r.Context()) != nil {
		return true
	}
	http.Error(w, "conversation analytics unavailable", http.StatusServiceUnavailable)
	return false
}

// analyticsRoute makes one strict source decision for the entire response.
// Missing history never silently falls back to platform analytics.
func (m *Module) analyticsRoute(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var history *HistoryAnalytics
		if m.deps.HistoryAnalytics != nil {
			history = m.deps.HistoryAnalytics()
		}
		reader, release, reason := history.projection(r.Context())
		if reader == nil {
			setAnalyticsSource(w.Header(), "statev2", reason)
			http.Error(w, "Conversation analytics unavailable ("+reason+"). Check history ingestion in Background tasks.", http.StatusServiceUnavailable)
			return
		}
		defer release()
		if history.validated != nil {
			history.validated()
		}
		setAnalyticsSource(w.Header(), "statev2", "")
		if history.CheckedAt != nil {
			if at := history.CheckedAt(); !at.IsZero() {
				w.Header().Set("X-Ocman-Analytics-Checked-At", at.UTC().Format(time.RFC3339))
			}
		}
		next(w, r.WithContext(context.WithValue(r.Context(), analyticsSourceKey{}, reader)))
	}
}

func setAnalyticsSource(header http.Header, source, reason string) {
	header.Set("X-Ocman-Analytics-Source", source)
	// Preserve the daily-activity experiment's diagnostic header.
	header.Set("X-Ocman-Activity-Source", source)
	if reason != "" {
		header.Set("X-Ocman-Analytics-Error", reason)
	}
}

func (m *Module) Close() {
	if m.deps.HistoryAnalytics == nil {
		return
	}
	h := m.deps.HistoryAnalytics()
	if h == nil {
		return
	}
	h.projections.mu.Lock()
	defer h.projections.mu.Unlock()
	for _, reader := range h.projections.readers {
		_ = reader.Close()
	}
	h.projections.readers = nil
}

// Launcher registration pairs the reader cleanup with listener shutdown.
// Readers themselves are opened lazily only after imports become ready.
func (m *Module) Launch(context.Context) error { return nil }
