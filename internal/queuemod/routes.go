package queuemod

import (
	"net/http"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/state"
)

// queuedMessageView is the wire shape for a pending follow-up. Images are
// omitted from the list payload (only their presence matters for the UI);
// the full send options ride along so a future "edit" could reuse them.
type MessageView struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	HasImages bool   `json:"hasImages"`
	Model     string `json:"model,omitempty"`
	Agent     string `json:"agent,omitempty"`
	Reasoning string `json:"reasoning,omitempty"`
	CreatedAt int64  `json:"createdAt"`
}

type queuedMessageView = MessageView

// handleSessionQueueList serves GET /api/session/{id}/queue — the pending
// follow-up messages waiting for the session's next idle edge (#58).
func (s *Module) List(w http.ResponseWriter, r *http.Request) {
	sessionID, ok := httpx.SessionID(w, r)
	if !ok {
		return
	}
	// The platform parameter is optional on this read-only endpoint, so
	// a client that only has the bare session id falls back to the
	// explicit cross-platform list (the one documented exception —
	// see queuesvc.Service.ListAnyPlatform).
	var msgs []state.QueuedMessage
	var err error
	if platform := httpx.PlatformHint(r); platform != "" {
		msgs, err = s.queue.List(r.Context(), platform, sessionID)
	} else {
		msgs, err = s.queue.ListAnyPlatform(r.Context(), sessionID)
	}
	if err != nil {
		log.WithError(err).WithField("session", sessionID).Error("listing message queue")
		http.Error(w, "failed to list queue", http.StatusInternalServerError)
		return
	}
	out := make([]queuedMessageView, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, toQueuedMessageView(m))
	}
	httpx.WriteJSON(w, mergeQueued(out, s.nativeQueuedViews(r.Context(), httpx.PlatformHint(r), sessionID)))
}

// handleSessionQueueDelete serves DELETE /api/session/{id}/queue/{qmid} —
// drop a queued follow-up without sending it. Best-effort: an already-gone
// item (it flushed, or another client deleted it) is a satisfied delete,
// so a missing id still returns 204 rather than 404. This also removes the
// click-delete-just-as-it-drains race.
func (s *Module) Delete(w http.ResponseWriter, r *http.Request) {
	sessionID, ok := httpx.SessionID(w, r)
	if !ok {
		return
	}
	qmid := r.PathValue("qmid")
	if !httpx.ValidID(qmid) {
		http.Error(w, "invalid queued message ID", http.StatusBadRequest)
		return
	}
	if handled, err := s.cancelNativeQueued(r.Context(), httpx.PlatformHint(r), sessionID, qmid); handled {
		if err != nil {
			log.WithError(err).WithField("queued", qmid).Error("cancelling native follow-up")
			http.Error(w, "failed to remove queued message", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if _, err := s.queue.Remove(r.Context(), sessionID, qmid); err != nil {
		log.WithError(err).WithField("queued", qmid).Error("removing queued message")
		http.Error(w, "failed to remove queued message", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSessionQueueMove serves POST /api/session/{id}/queue/{qmid}/move —
// reorder a queued follow-up up (-1) or down (+1) in the queue.
func (s *Module) Move(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Direction int `json:"direction"`
	}
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
		return
	}
	if req.Direction != -1 && req.Direction != 1 {
		http.Error(w, "direction must be -1 or 1", http.StatusBadRequest)
		return
	}
	sessionID, ok := httpx.SessionID(w, r)
	if !ok {
		return
	}
	qmid := r.PathValue("qmid")
	if !httpx.ValidID(qmid) {
		http.Error(w, "invalid queued message ID", http.StatusBadRequest)
		return
	}
	if _, err := s.queue.Move(r.Context(), sessionID, qmid, req.Direction); err != nil {
		log.WithError(err).WithField("queued", qmid).Error("moving queued message")
		http.Error(w, "failed to move queued message", http.StatusInternalServerError)
		return
	}
	// A boundary move (already first/last) or unknown id is a no-op.
	w.WriteHeader(http.StatusNoContent)
}

func toQueuedMessageView(m state.QueuedMessage) queuedMessageView {
	return queuedMessageView{
		ID:        m.ID,
		Text:      m.Text,
		HasImages: m.ImagesJSON != "",
		Model:     m.Model,
		Agent:     m.Agent,
		Reasoning: m.Reasoning,
		CreatedAt: m.CreatedAt,
	}
}
