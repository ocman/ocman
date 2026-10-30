package codexprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/providerprotocol"
)

const rpcTimeout = 30 * time.Second

type statusError struct {
	status int
	err    error
}

func (e statusError) Error() string { return e.err.Error() }
func (e statusError) Unwrap() error { return e.err }

func errStatus(err error) int {
	var se statusError
	switch {
	case errors.As(err, &se):
		return se.status
	case errors.Is(err, platforms.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, platforms.ErrBusy):
		return http.StatusConflict
	case errors.Is(err, platforms.ErrUnsupported):
		return http.StatusNotImplemented
	}
	return http.StatusInternalServerError
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func reply(w http.ResponseWriter, value any, err error) {
	switch {
	case errors.Is(err, platforms.ErrSessionOwnedElsewhere):
		writeJSON(w, errStatus(err), providerprotocol.Error{Code: providerprotocol.CodeSessionOwnedElsewhere, Message: err.Error()})
	case errors.Is(err, platforms.ErrCreationRejected):
		writeJSON(w, errStatus(err), providerprotocol.Error{Code: providerprotocol.CodeCreationRejected, Message: err.Error()})
	case errors.Is(err, platforms.ErrReplyUnrecorded):
		writeJSON(w, errStatus(err), providerprotocol.Error{Code: providerprotocol.CodeReplyUnrecorded, Message: err.Error()})
	case err != nil:
		writeJSON(w, errStatus(err), providerprotocol.Error{Message: err.Error()})
	case value == nil:
		w.WriteHeader(http.StatusNoContent)
	default:
		writeJSON(w, http.StatusOK, value)
	}
}

func decode(r *http.Request, value any) error {
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 64<<20)).Decode(value); err != nil {
		return statusError{http.StatusBadRequest, fmt.Errorf("invalid request: %w", err)}
	}
	return nil
}

// All execution routes belong to ACP. The native client only serves history,
// catalogs and metadata; it has no route that can load or prompt a writer.
func (b *backend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if b.acp.serve(w, r) {
		return
	}
	if r.Method == http.MethodGet {
		ctx, cancel := context.WithTimeout(r.Context(), rpcTimeout)
		defer cancel()
		b.serveRead(ctx, w, r)
		return
	}
	if kind, rest, ok := promptRoute(r.URL.Path); ok {
		reply(w, nil, b.servePrompt(r, kind, rest))
		return
	}
	reply(w, nil, platforms.ErrUnsupported)
}

func promptRoute(path string) (kind, rest string, ok bool) {
	for _, kind := range []string{"permissions", "questions"} {
		if rest, ok := strings.CutPrefix(path, "/"+kind+"/"); ok {
			return kind, rest, true
		}
	}
	return "", "", false
}

func (b *backend) servePrompt(r *http.Request, kind, rest string) error {
	id, action, _ := strings.Cut(rest, "/")
	switch {
	case kind == "permissions" && action == "reply":
		var req platforms.RespondPermissionRequest
		if err := decode(r, &req); err != nil {
			return err
		}
		return b.replyPermission(id, req.Reply)
	case kind == "questions" && action == "reply":
		var req platforms.RespondQuestionRequest
		if err := decode(r, &req); err != nil {
			return err
		}
		return b.replyQuestion(id, req.Answers)
	case kind == "questions" && action == "reject":
		return b.rejectQuestion(id)
	}
	return platforms.ErrUnsupported
}
