package providersupervisor

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/providerprotocol"
)

// maxRequestBytes bounds one decoded backend request body.
const maxRequestBytes = 64 << 20

// StatusError carries the protocol status of an explicit, user-facing error.
type StatusError struct {
	Status int
	Err    error
}

func (e StatusError) Error() string { return e.Err.Error() }
func (e StatusError) Unwrap() error { return e.Err }

// Replies writes backend protocol responses. Status maps a backend's own
// errors to a protocol status, returning 0 for errors it does not know; it
// runs after StatusError and before the shared platform errors.
type Replies struct {
	Status func(error) int
}

// ErrStatus is the protocol status of err.
func (r Replies) ErrStatus(err error) int {
	var se StatusError
	if errors.As(err, &se) {
		return se.Status
	}
	if r.Status != nil {
		if status := r.Status(err); status != 0 {
			return status
		}
	}
	switch {
	case errors.Is(err, platforms.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, platforms.ErrBusy):
		return http.StatusConflict
	case errors.Is(err, platforms.ErrUnsupported):
		return http.StatusNotImplemented
	}
	return http.StatusInternalServerError
}

// Reply writes value, 204 for a nil value, or err with its protocol code.
func (r Replies) Reply(w http.ResponseWriter, value any, err error) {
	switch {
	case err != nil:
		WriteJSON(w, r.ErrStatus(err), providerprotocol.Error{Code: errorCode(err), Message: err.Error()})
	case value == nil:
		w.WriteHeader(http.StatusNoContent)
	default:
		WriteJSON(w, http.StatusOK, value)
	}
}

func errorCode(err error) string {
	switch {
	case errors.Is(err, platforms.ErrSessionOwnedElsewhere):
		return providerprotocol.CodeSessionOwnedElsewhere
	case errors.Is(err, platforms.ErrCreationRejected):
		return providerprotocol.CodeCreationRejected
	case errors.Is(err, platforms.ErrReplyUnrecorded):
		return providerprotocol.CodeReplyUnrecorded
	}
	return ""
}

// WriteJSON writes value as a JSON response with status.
func WriteJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// Decode reads a JSON request body into value; a malformed body is a 400.
func Decode(r *http.Request, value any) error {
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxRequestBytes)).Decode(value); err != nil {
		return StatusError{http.StatusBadRequest, fmt.Errorf("invalid request: %w", err)}
	}
	return nil
}

// WithStatus attaches a protocol status to err.
func WithStatus(status int, err error) error { return StatusError{Status: status, Err: err} }
