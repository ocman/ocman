package sessionlifecycle

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
	log "github.com/sirupsen/logrus"
)

func (m *Module) draftRoutes(r module.Router) {
	r.Local("GET /api/session-drafts", m.listDrafts)
	r.Local("GET /api/session-drafts/{id}", m.getDraft)
	r.Local("PUT /api/session-drafts/{id}", m.saveDraft)
	r.Local("DELETE /api/session-drafts/{id}", m.deleteDraft)
	r.Local("POST /api/session-drafts/{id}/start", m.acceptDraft)
	r.Local("POST /api/session-drafts/{id}/messages", m.enqueueDraft)
	r.Local("POST /api/session-drafts/{id}/attachment", m.draftAttachment)
	r.Local("POST /api/session-drafts/{id}/retry-delivery", m.retryDraftDelivery)
}

func (m *Module) Start(ctx context.Context) error {
	m.workers.Bind(ctx)
	if m.d.DB == nil {
		return nil
	}
	err := (draftStore{m.d.DB}).recover(ctx)
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		return nil
	}
	return err
}

func (m *Module) Run(ctx context.Context) { <-ctx.Done(); m.workers.Close() }
func (m *Module) Finalize()               { m.workers.Close() }

func (m *Module) draftEnabled(w http.ResponseWriter) bool {
	if m.d.DB != nil {
		return true
	}
	http.Error(w, "draft persistence unavailable", http.StatusServiceUnavailable)
	return false
}

func (m *Module) listDrafts(w http.ResponseWriter, r *http.Request) {
	if !m.draftEnabled(w) {
		return
	}
	drafts, err := (draftStore{m.d.DB}).list(r.Context())
	if err != nil {
		httpx.ServerError(w, "reading drafts", err)
		return
	}
	httpx.WriteJSON(w, drafts)
}

func (m *Module) getDraft(w http.ResponseWriter, r *http.Request) {
	if !m.draftEnabled(w) {
		return
	}
	draft, err := (draftStore{m.d.DB}).get(r.Context(), r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		httpx.ServerError(w, "reading draft", err)
		return
	}
	httpx.WriteJSON(w, draft)
}

func (m *Module) saveDraft(w http.ResponseWriter, r *http.Request) {
	if !m.draftEnabled(w) {
		return
	}
	id := r.PathValue("id")
	var meta json.RawMessage
	if !httpx.ReadJSON(w, r, httpx.MaxSendMessageBody, &meta) {
		return
	}
	var target Target
	if !sessioncatalog.IsID(id) || json.Unmarshal(meta, &target) != nil {
		http.Error(w, "invalid draft", http.StatusBadRequest)
		return
	}
	if !target.Validate(w) {
		return
	}
	draft, err := (draftStore{m.d.DB}).saveVersion(r.Context(), id, meta, r.Header.Get("If-Match"))
	if errors.Is(err, errDraftVersionConflict) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if err != nil {
		httpx.ServerError(w, "saving draft", err)
		return
	}
	httpx.WriteJSONStatus(w, http.StatusOK, draft)
}

func (m *Module) deleteDraft(w http.ResponseWriter, r *http.Request) {
	if !m.draftEnabled(w) {
		return
	}
	// Failed setup can be retired on the hub without contacting an outdated or
	// disconnected owner. A confirmed native session must use session actions.
	result, err := m.d.DB.ExecContext(r.Context(), `UPDATE conversation_draft
		SET phase='discarded',metadata='{}',request='',result='',steps='{}',error=''
		WHERE id=? AND (phase IN ('draft','discarded') OR
		(phase IN ('failed','interrupted') AND coalesce(json_extract(CASE WHEN json_valid(result) THEN result ELSE '{}' END,'$.sessionId'),'')<>id))`, r.PathValue("id"))
	if err != nil {
		httpx.ServerError(w, "discarding draft", err)
		return
	}
	n, err := result.RowsAffected()
	if err != nil {
		httpx.ServerError(w, "discarding draft", err)
		return
	}
	if n == 0 {
		http.Error(w, "draft is already submitted or missing", http.StatusConflict)
		return
	}
	m.draftChanged()
	w.WriteHeader(http.StatusNoContent)
}

func (m *Module) acceptDraft(w http.ResponseWriter, r *http.Request) {
	if !m.draftEnabled(w) {
		return
	}
	var req startRequest
	if !httpx.ReadJSON(w, r, httpx.MaxSendMessageBody, &req) {
		return
	}
	id := r.PathValue("id")
	if !sessioncatalog.IsID(id) || req.Send == nil || (req.Send.Message == "" && len(req.Send.Images) == 0) {
		http.Error(w, "a draft identity and first prompt are required", http.StatusBadRequest)
		return
	}
	if !req.Validate(w) || !req.ResolvePlatform(r.Context(), m, w, req.Send.Model) {
		return
	}
	req.DraftID, req.StartID = id, id
	raw, err := json.Marshal(req)
	if err != nil {
		httpx.ServerError(w, "encoding submission", err)
		return
	}
	store := draftStore{m.d.DB}
	draft, created, err := store.accept(r.Context(), id, raw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if errors.Is(err, errDraftSubmissionConflict) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		httpx.ServerError(w, "persisting first submission", err)
		return
	}
	if created {
		// The body has committed. Work belongs to the module, not this HTTP client.
		if !m.workers.Go(func(ctx context.Context) { m.runDraftWorker(ctx, id, func() { m.launchDraft(ctx, id, raw) }) }) {
			_ = store.finish(r.Context(), id, nil, "Setup was not started because the server is stopping.")
		}
	}
	httpx.WriteJSONStatus(w, http.StatusAccepted, draft)
}

// launchDraft uses the same creation orchestration as existing API callers.
// Capture its typed JSON response without keeping an HTTP connection open.
func (m *Module) launchDraft(ctx context.Context, id string, raw []byte) {
	store := draftStore{m.d.DB}
	select {
	case m.launchSlots <- struct{}{}:
		defer func() { <-m.launchSlots }()
	case <-ctx.Done():
		return
	}
	if _, err := m.d.DB.ExecContext(ctx, `UPDATE conversation_draft SET phase='starting' WHERE id=? AND phase='accepted'`, id); err != nil {
		return
	}
	response := httptest.NewRecorder()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "/api/sessions/start", bytes.NewReader(raw))
	if err != nil {
		return
	}
	m.HandleStartSession(response, request)
	var result struct{ SessionID, FirstMessageError string }
	var message string
	if response.Code >= 400 || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.SessionID != id {
		message = "Session setup failed. " + strings.TrimSpace(response.Body.String())
	} else {
		message = result.FirstMessageError
	}
	write, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := store.finish(write, id, response.Body.Bytes(), message); err != nil {
		log.WithError(err).Error("persisting session setup outcome")
	}
	m.draftChanged()
	if message == "" && m.d.DispatchQueued != nil {
		var first startRequest
		if json.Unmarshal(raw, &first) == nil {
			m.d.DispatchQueued(ctx, first.Platform, id)
		}
	}
}

func (m *Module) draftChanged() {
	if m.d.Broadcast != nil {
		m.d.Broadcast("ocman.session.draft.updated", []byte(`{}`))
	}
}

// QueueBlocked fences sweeps and idle events until first delivery succeeds.
func (m *Module) QueueBlocked(ctx context.Context, id string) bool {
	if m.d.DB == nil {
		return false
	}
	d, err := (draftStore{m.d.DB}).get(ctx, id)
	return !errors.Is(err, sql.ErrNoRows) && (err != nil || d.Phase != "ready")
}

func (m *Module) enqueueDraft(w http.ResponseWriter, r *http.Request) {
	if !m.draftEnabled(w) {
		return
	}
	var req platforms.SendMessageRequest
	if !httpx.ReadJSON(w, r, httpx.MaxSendMessageBody, &req) {
		return
	}
	draft, err := (draftStore{m.d.DB}).get(r.Context(), r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var first startRequest
	if json.Unmarshal(draft.Request, &first) != nil || draft.Phase == "draft" {
		http.Error(w, "submit the first prompt before queueing follow-ups", http.StatusConflict)
		return
	}
	// Explicit ownership stays authoritative even before native allocation.
	if _, ok := m.d.Owner(w, first.Directory, first.RemoteID); !ok {
		return
	}
	req.SessionID = draft.ID
	if m.d.Enqueue == nil {
		http.Error(w, "queue unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := m.d.Enqueue(r.Context(), first.Platform, req); err != nil {
		httpx.ServerError(w, "queueing follow-up", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
