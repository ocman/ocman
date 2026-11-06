package sessionlifecycle

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/httpx"
)

// An explicit retry repeats only delivery on the already bound session, never
// worktree setup or native allocation. Uncertain creation remains fail-closed.
func (m *Module) retryDraftDelivery(w http.ResponseWriter, r *http.Request) {
	if !m.draftEnabled(w) {
		return
	}
	id := r.PathValue("id")
	store := draftStore{m.d.DB}
	draft, err := store.get(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var request startRequest
	var result map[string]any
	if draft.Phase != "failed" && draft.Phase != "interrupted" || json.Unmarshal(draft.Request, &request) != nil || request.Send == nil ||
		json.Unmarshal(draft.Result, &result) != nil || result["sessionId"] != id {
		http.Error(w, "there is no confirmed session on which to retry delivery", http.StatusConflict)
		return
	}
	if !request.ResolvePlatform(r.Context(), m, w, request.Send.Model) {
		return
	}
	claimed, err := m.d.DB.ExecContext(r.Context(), `UPDATE conversation_draft SET phase='delivering',error='' WHERE id=? AND phase IN ('failed','interrupted')`, id)
	if err != nil {
		httpx.ServerError(w, "claiming first delivery", err)
		return
	}
	n, err := claimed.RowsAffected()
	if err != nil || n != 1 {
		http.Error(w, "first delivery is already being retried", http.StatusConflict)
		return
	}
	if !m.workers.Go(func(ctx context.Context) {
		m.runDraftWorker(ctx, id, func() {
			ctx = hostsvc.WithProgress(ctx, m.startProgress(ctx, id))
			err := m.deliverFirst(ctx, request, request.Platform, id)
			message := ""
			if err != nil {
				message = err.Error()
			}
			result["firstMessageSent"], result["firstMessageError"] = err == nil, message
			raw, _ := json.Marshal(result)
			write, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if err := store.finish(write, id, raw, message); err != nil {
				log.WithError(err).Error("persisting first-delivery retry")
			}
			m.draftChanged()
			if err == nil && m.d.DispatchQueued != nil {
				m.d.DispatchQueued(ctx, request.Platform, id)
			}
		})
	}) {
		_ = store.finish(r.Context(), id, draft.Result, "Server stopped before retrying delivery.")
	}
	w.WriteHeader(http.StatusAccepted)
}
