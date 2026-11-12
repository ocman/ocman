package sessionlifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"hash/crc32"
	"net/http"
	"time"

	"github.com/ocman/ocman/internal/httpx"
)

// Bounded striped locks order archival against durable completion without
// retaining one lock per historical draft. Workers never hold these during setup.
func (m *Module) lockDraftOutcome(id string) func() {
	lock := &m.draftOutcomeLocks[crc32.ChecksumIEEE([]byte(id))%uint32(len(m.draftOutcomeLocks))]
	lock.Lock()
	return lock.Unlock
}

func (m *Module) finishDraft(ctx context.Context, id string, result []byte, message string) error {
	unlock := m.lockDraftOutcome(id)
	defer unlock()
	// An archive may spend its read deadline on the owner. Receipt persistence
	// gets its own bounded lifetime after acquiring the outcome lock.
	write, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return (draftStore{m.d.DB}).finish(write, id, result, message)
}

// Compact owner reads need the same setup hold without per-row draft queries.
func (m *Module) ArchivedSetupIDs(ctx context.Context) (map[string]bool, error) {
	rows, err := m.d.DB.QueryContext(ctx, `SELECT id FROM conversation_draft WHERE phase IN ('accepted','starting','delivering')
		AND id IN (SELECT session_id FROM archived_session)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids[id] = true
	}
	return ids, rows.Err()
}

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
	unlock := m.lockDraftOutcome(r.PathValue("id"))
	defer unlock()
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
	watermark := int64(1)
	if draft.Phase != "accepted" && draft.Phase != "starting" && draft.Phase != "delivering" {
		if m.d.ArchiveWatermark == nil {
			http.Error(w, "archive watermark unavailable", http.StatusServiceUnavailable)
			return
		}
		watermark, err = m.d.ArchiveWatermark(r.Context(), first.Platform, draft.ID)
		if err != nil {
			httpx.ServerError(w, "reading archive watermark", err)
			return
		}
	}
	if err := m.d.Archive(r.Context(), first.Platform, draft.ID, watermark, true); err != nil {
		httpx.ServerError(w, "archiving setup", err)
		return
	}
	if _, err := m.d.DB.ExecContext(r.Context(), `UPDATE conversation_draft SET archived_error_version=? WHERE id=?`, draft.ErrorVersion, draft.ID); err != nil {
		httpx.ServerError(w, "acknowledging archived failure", err)
		return
	}
	m.draftChanged()
	w.WriteHeader(http.StatusNoContent)
}
