package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/queuesvc"
	"github.com/NoUseFreak/ocman/internal/sessionsvc"
)

// writeJSON writes a JSON response with an implicit 200 status.
func writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.WithError(err).Error("failed to encode JSON response")
	}
}

// writeJSONStatus writes a JSON response with an explicit status code.
// Callers must not call WriteHeader themselves first: the header map is
// flushed by WriteHeader, so a Content-Type set afterwards is silently
// dropped and the client sniffs the type instead.
func writeJSONStatus(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.WithError(err).Error("failed to encode JSON response")
	}
}

// writeCancellation keeps abandoned requests out of server-error telemetry,
// including the gRPC status returned by remote Host and Platform adapters.
// 499 is the conventional client-closed-request status; deadlines still use
// each handler's failure path because they indicate work that took too long.
func writeCancellation(w http.ResponseWriter, msg string, err error) bool {
	if !errors.Is(err, context.Canceled) && status.Code(err) != codes.Canceled {
		return false
	}
	log.WithError(err).Debug(msg)
	http.Error(w, "request canceled", 499)
	return true
}

// serverError logs the real error and returns a generic message to the client.
func serverError(w http.ResponseWriter, msg string, err error) {
	if writeCancellation(w, msg, err) {
		return
	}
	log.WithError(err).Error(msg)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

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
	writePlatformError(w, msg, err)
}

// writePlatformError maps a Platform error to an appropriate HTTP response.
func writePlatformError(w http.ResponseWriter, msg string, err error) {
	if writeCancellation(w, msg, err) {
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
	if errors.Is(err, platforms.ErrBusy) {
		http.Error(w, "session is currently processing a prompt; try again in a moment", http.StatusConflict)
		return
	}
	if errors.Is(err, platforms.ErrPlatformUnreachable) {
		http.Error(w, "no running platform instance for this location", http.StatusServiceUnavailable)
		return
	}
	if errors.Is(err, ocapi.ErrAuthentication) {
		log.WithError(err).Error(msg)
		http.Error(w, "OpenCode authentication failed; check the configured server password", http.StatusBadGateway)
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
