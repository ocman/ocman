package sessionlifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/ocman/ocman/internal/httpx"
)

// SetupPending prevents a newly published, still-idle native session from
// looking like a completed response before its initial payload is delivered.
func (m *Module) SetupPending(ctx context.Context, id string) bool {
	if m.d.DB == nil {
		return false
	}
	var phase string
	err := m.d.DB.QueryRowContext(ctx, `SELECT phase FROM conversation_draft WHERE id=?`, id).Scan(&phase)
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	return err != nil || phase == "accepted" || phase == "starting" || phase == "delivering"
}

// Archiving accepted work hides it without cancelling setup or dropping payloads.
// Use the same archive marker as the future native-backed session, so normal
// completion and permission/question resurfacing continue after publication.
func (m *Module) archiveDraft(w http.ResponseWriter, r *http.Request) {
	if !m.draftEnabled(w) {
		return
	}
	draft, err := (draftStore{m.d.DB}).get(r.Context(), r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		httpx.ServerError(w, "reading setup", err)
		return
	}
	var first startRequest
	if draft.Phase == "draft" || draft.Phase == "discarded" || json.Unmarshal(draft.Request, &first) != nil || first.Platform == "" {
		http.Error(w, "submit the first prompt before archiving", http.StatusConflict)
		return
	}
	if m.d.Archive == nil {
		http.Error(w, "archive unavailable", http.StatusServiceUnavailable)
		return
	}
	// Before native publication there is no owner-clock update timestamp. Use
	// its minimum watermark, not the hub's submittedAt, which can be ahead of a
	// remote clock. SetupPending fences publication until first delivery.
	if err := m.d.Archive(r.Context(), first.Platform, draft.ID, 1, true); err != nil {
		httpx.ServerError(w, "archiving setup", err)
		return
	}
	m.draftChanged()
	w.WriteHeader(http.StatusNoContent)
}
