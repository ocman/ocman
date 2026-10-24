package server

import (
	"errors"
	"net/http"

	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/queuesvc"
	"github.com/NoUseFreak/ocman/internal/sessionsvc"
)

// writeSessionSvcError maps a sessionsvc error to an HTTP response.
func writeSessionSvcError(w http.ResponseWriter, msg string, err error) {
	var ve *sessionsvc.ValidationError
	if errors.As(err, &ve) {
		http.Error(w, ve.Error(), http.StatusBadRequest)
		return
	}
	if errors.Is(err, queuesvc.ErrEmptyMessage) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if errors.Is(err, sessionsvc.ErrNoPlatformAvailable) {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	httpx.WritePlatformError(w, msg, err)
}
