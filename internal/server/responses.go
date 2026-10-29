package server

import (
	"errors"
	"net/http"

	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/queuesvc"
	"github.com/ocman/ocman/internal/sessionsvc"
)

// writeSessionSvcError maps a sessionsvc error to an HTTP response.
func writeSessionSvcError(w http.ResponseWriter, msg string, err error) {
	var ve *sessionsvc.ValidationError
	if errors.As(err, &ve) {
		http.Error(w, ve.Error(), http.StatusBadRequest)
		return
	}
	if errors.Is(err, queuesvc.ErrEmptyMessage) || errors.Is(err, queuesvc.ErrModelPlatform) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if errors.Is(err, sessionsvc.ErrNoPlatformAvailable) {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	httpx.WritePlatformError(w, msg, err)
}
