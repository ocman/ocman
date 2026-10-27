package sessionlifecycle

import (
	"net/http"

	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func (m *Module) HandleBackgroundTasks(w http.ResponseWriter, r *http.Request) {
	m.d.WithSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, id string, p platforms.Platform) {
		manager, ok := p.(platforms.BackgroundTaskManager)
		if !ok || !p.Capabilities().BackgroundTaskLifecycle {
			writeSessionError(w, "reading background tasks", platforms.ErrUnsupported)
			return
		}
		snapshot, err := manager.BackgroundTasks(r.Context(), id)
		if err != nil {
			writeSessionError(w, "reading background tasks", err)
			return
		}
		httpx.WriteJSON(w, snapshot)
	})
}

func (m *Module) HandleBackgroundLifecycle(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.SessionID(w, r)
	if !ok {
		return
	}
	var body struct {
		Lifecycle string `json:"lifecycle"`
	}
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &body) {
		return
	}
	err := m.d.Sessions().SetBackgroundLifecycle(r.Context(), platformHint(r), platforms.SetBackgroundLifecycleRequest{
		SessionID: id, TaskID: r.PathValue("taskID"), Lifecycle: body.Lifecycle,
	})
	if err != nil {
		writeSessionError(w, "classifying background task", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
