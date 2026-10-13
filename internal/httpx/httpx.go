// Package httpx holds the JSON and request-body helpers shared by the server
// and feature modules, so a module's handlers answer exactly like the server's.
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// MaxRequestBody is the default bound for a buffered JSON request body (1 MB).
const MaxRequestBody = 1 << 20

// MaxSendMessageBody bounds a body that carries a message with inline images
// (20 MB).
const MaxSendMessageBody = 20 << 20

// BodyReadTimeout bounds how long a handler waits for a bounded,
// fully-buffered request body. Applied per handler rather than as
// http.Server.ReadTimeout, which would also cut off SSE streams,
// WebSocket terminals, and multi-megabyte uploads. A var so tests can
// shrink it.
var BodyReadTimeout = 30 * time.Second

// WriteJSON writes a JSON response with an implicit 200 status.
func WriteJSON(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.WithError(err).Error("failed to encode JSON response")
	}
}

// WriteJSONStatus writes a JSON response with an explicit status code.
// Callers must not call WriteHeader themselves first: the header map is
// flushed by WriteHeader, so a Content-Type set afterwards is silently
// dropped and the client sniffs the type instead.
func WriteJSONStatus(w http.ResponseWriter, code int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.WithError(err).Error("failed to encode JSON response")
	}
}

// WriteCancellation keeps abandoned requests out of server-error telemetry,
// including the gRPC status returned by remote Host and Platform adapters.
// 499 is the conventional client-closed-request status; deadlines still use
// each handler's failure path because they indicate work that took too long.
func WriteCancellation(w http.ResponseWriter, msg string, err error) bool {
	if !errors.Is(err, context.Canceled) && status.Code(err) != codes.Canceled {
		return false
	}
	log.WithError(err).Debug(msg)
	http.Error(w, "request canceled", 499)
	return true
}

// ServerError logs the real error and returns a generic message to the client.
func ServerError(w http.ResponseWriter, msg string, err error) {
	if WriteCancellation(w, msg, err) {
		return
	}
	log.WithError(err).Error(msg)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

// SetBodyReadDeadline bounds the time spent reading this request's body,
// so a client that trickles bytes can't pin a goroutine indefinitely.
// No-op when the ResponseWriter doesn't support it (e.g. httptest
// recorders), which is why the error is dropped.
func SetBodyReadDeadline(w http.ResponseWriter, d time.Duration) {
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(d))
}

// ClearBodyReadDeadline drops the deadline once the body is in hand, so
// a handler that then does slow work (git, tmux, an upstream agent call)
// isn't affected by a leftover deadline on the connection.
func ClearBodyReadDeadline(w http.ResponseWriter) {
	_ = http.NewResponseController(w).SetReadDeadline(time.Time{})
}

// ReadJSON reads the request body (up to maxBytes) and unmarshals it into
// dst. Returns false and writes an HTTP error if reading or parsing fails.
func ReadJSON(w http.ResponseWriter, r *http.Request, maxBytes int64, dst any) bool {
	SetBodyReadDeadline(w, BodyReadTimeout)
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBytes))
	if err != nil {
		// Deliberately leave the (expired) deadline in place: clearing
		// it here would make net/http block forever draining the body
		// the client never finished sending.
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return false
	}
	ClearBodyReadDeadline(w)
	if err := json.Unmarshal(body, dst); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return false
	}
	return true
}
