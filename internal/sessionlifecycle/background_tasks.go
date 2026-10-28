package sessionlifecycle

import (
	"context"
	"net/http"
	"time"

	"github.com/ocman/ocman/internal/httpx"

	"github.com/ocman/ocman/internal/platforms"
)

func (m *Module) HandleBackgroundTaskStop(w http.ResponseWriter, r *http.Request) {
	sessionID, ok := httpx.SessionID(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)
	defer cancel()
	taskID := r.PathValue("taskID")
	if taskID == "" {
		http.Error(w, "task ID is required", http.StatusBadRequest)
		return
	}
	if err := m.d.Sessions().Abort(ctx, platformHint(r), platforms.AbortRequest{SessionID: sessionID, TaskID: taskID}); err != nil {
		writeSessionError(w, "stopping background task", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
