package httpx

import (
	"errors"
	"net/http"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// WritePlatformError maps a Platform error to an appropriate HTTP response.
func WritePlatformError(w http.ResponseWriter, msg string, err error) {
	if WriteCancellation(w, msg, err) {
		return
	}
	if errors.Is(err, platforms.ErrUnsupported) {
		http.Error(w, "operation not supported by this platform", http.StatusNotImplemented)
		return
	}
	if errors.Is(err, platforms.ErrNotFound) {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	if errors.Is(err, platforms.ErrHistoryUnavailable) {
		w.Header().Set("X-Ocman-Conversation-Error", "history-unavailable")
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	if errors.Is(err, platforms.ErrBusy) {
		http.Error(w, "session is currently processing a prompt; try again in a moment", http.StatusConflict)
		return
	}
	if errors.Is(err, platforms.ErrPlatformUnreachable) {
		http.Error(w, "no running platform instance for this location", http.StatusServiceUnavailable)
		return
	}
	if errors.Is(err, platforms.ErrPlatformUnauthenticated) {
		log.WithError(err).Error(msg)
		http.Error(w, "platform authentication failed; check its configured credentials", http.StatusBadGateway)
		return
	}
	if errors.Is(err, platforms.ErrUpstreamRejected) {
		log.WithError(err).Warn(msg)
		var ue *platforms.UpstreamError
		body := "the platform rejected the request"
		if errors.As(err, &ue) && ue.Message != "" {
			body = ue.Message
		}
		http.Error(w, body, http.StatusUnprocessableEntity)
		return
	}
	log.WithError(err).Error(msg)
	http.Error(w, "failed to reach platform instance", http.StatusBadGateway)
}
