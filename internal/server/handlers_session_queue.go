package server

import (
	"net/http"
	"strings"

	"github.com/NoUseFreak/ocman/internal/state"
)

// queuedMessageView is the wire shape for a pending follow-up. Images are
// omitted from the list payload (only their presence matters for the UI);
// the full send options ride along so a future "edit" could reuse them.
type queuedMessageView struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	HasImages bool   `json:"hasImages"`
	Model     string `json:"model,omitempty"`
	Agent     string `json:"agent,omitempty"`
	Reasoning string `json:"reasoning,omitempty"`
	CreatedAt int64  `json:"createdAt"`
}

// handleSessionQueueList serves GET /api/session/{id}/queue — the pending
// follow-up messages waiting for the session's next idle edge (#58).
func (s *Server) handleSessionQueueList(w http.ResponseWriter, r *http.Request) {
	s.withSessionPath(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, _ string) {
		msgs, err := s.queueSvc().List(platformHint(r), sessionID)
		if err != nil {
			http.Error(w, "failed to list queue", http.StatusInternalServerError)
			return
		}
		out := make([]queuedMessageView, 0, len(msgs))
		for _, m := range msgs {
			out = append(out, toQueuedMessageView(m))
		}
		writeJSON(w, out)
	})
}

// handleSessionQueueDelete serves DELETE /api/session/{id}/queue/{qmid} —
// drop a queued follow-up without sending it. Best-effort: an already-gone
// item (it flushed, or another client deleted it) is a satisfied delete,
// so a missing id still returns 204 rather than 404. This also removes the
// click-delete-just-as-it-drains race.
func (s *Server) handleSessionQueueDelete(w http.ResponseWriter, r *http.Request) {
	s.withSessionPath(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, rest string) {
		qmid := strings.TrimPrefix(rest, "queue/")
		if !validateID(qmid) {
			http.Error(w, "invalid queued message ID", http.StatusBadRequest)
			return
		}
		if _, err := s.queueSvc().Remove(sessionID, qmid); err != nil {
			http.Error(w, "failed to remove queued message", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// handleSessionQueueMove serves POST /api/session/{id}/queue/{qmid}/move —
// reorder a queued follow-up up (-1) or down (+1) in the queue.
func (s *Server) handleSessionQueueMove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Direction int `json:"direction"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	if req.Direction != -1 && req.Direction != 1 {
		http.Error(w, "direction must be -1 or 1", http.StatusBadRequest)
		return
	}
	s.withSessionPath(w, r, func(w http.ResponseWriter, r *http.Request, sessionID, rest string) {
		rest = strings.TrimPrefix(rest, "queue/")
		qmid := strings.TrimSuffix(rest, "/move")
		if !validateID(qmid) {
			http.Error(w, "invalid queued message ID", http.StatusBadRequest)
			return
		}
		moved, err := s.queueSvc().Move(sessionID, qmid, req.Direction)
		if err != nil {
			http.Error(w, "failed to move queued message", http.StatusInternalServerError)
			return
		}
		if !moved {
			// Boundary (already first/last) or unknown id — nothing to do.
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
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
