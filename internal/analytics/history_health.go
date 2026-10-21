package analytics

import (
	"net/http"
	"time"

	"github.com/NoUseFreak/ocman/internal/httpx"
)

// History health is diagnostic only. Neither an incomplete scan nor a failed
// session blocks analytics over the source's already committed projections.
func (m *Module) handleHistoryHealth(w http.ResponseWriter, _ *http.Request) {
	status := struct {
		Status        string `json:"status"`
		Sources       int    `json:"sources"`
		LastCheckedAt string `json:"lastCheckedAt,omitempty"`
	}{Status: "unavailable"}
	if m.deps.HistoryAnalytics != nil {
		if h := m.deps.HistoryAnalytics(); h != nil && h.Store != nil && h.Instances != nil {
			ids, complete := h.Instances()
			status.Sources = len(ids)
			status.Status = "syncing"
			if complete && len(ids) > 0 {
				status.Status = "ready"
			}
			if h.CheckedAt != nil {
				if at := h.CheckedAt(); !at.IsZero() {
					status.LastCheckedAt = at.UTC().Format(time.RFC3339)
				}
			}
		}
	}
	httpx.WriteJSON(w, status)
}
