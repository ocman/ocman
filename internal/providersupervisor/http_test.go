package providersupervisor

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/providerprotocol"
)

func TestRepliesErrStatus(t *testing.T) {
	errOwn := errors.New("backend specific")
	replies := Replies{Status: func(err error) int {
		if errors.Is(err, errOwn) {
			return http.StatusForbidden
		}
		return 0
	}}
	for _, tc := range []struct {
		err  error
		want int
	}{
		{WithStatus(http.StatusTeapot, platforms.ErrNotFound), http.StatusTeapot}, // explicit status wins
		{fmt.Errorf("wrapped: %w", errOwn), http.StatusForbidden},
		{platforms.ErrNotFound, http.StatusNotFound},
		{platforms.ErrBusy, http.StatusConflict},
		{platforms.ErrUnsupported, http.StatusNotImplemented},
		{errors.New("other"), http.StatusInternalServerError},
	} {
		if got := replies.ErrStatus(tc.err); got != tc.want {
			t.Errorf("ErrStatus(%v) = %d, want %d", tc.err, got, tc.want)
		}
	}
	if got := (Replies{}).ErrStatus(errOwn); got != http.StatusInternalServerError {
		t.Errorf("without a hook = %d", got)
	}
}

func TestRepliesReply(t *testing.T) {
	for _, tc := range []struct {
		name   string
		value  any
		err    error
		status int
		code   string
	}{
		{"value", map[string]int{"n": 1}, nil, http.StatusOK, ""},
		{"empty", nil, nil, http.StatusNoContent, ""},
		{"owned elsewhere", nil, &platforms.BusyError{Message: "owned", Cause: platforms.ErrSessionOwnedElsewhere}, http.StatusConflict, providerprotocol.CodeSessionOwnedElsewhere},
		{"creation rejected", nil, errors.Join(platforms.ErrCreationRejected, WithStatus(http.StatusBadRequest, errors.New("bad"))), http.StatusBadRequest, providerprotocol.CodeCreationRejected},
		{"reply unrecorded", nil, WithStatus(http.StatusServiceUnavailable, fmt.Errorf("%w: spool", platforms.ErrReplyUnrecorded)), http.StatusServiceUnavailable, providerprotocol.CodeReplyUnrecorded},
		{"plain", nil, errors.New("boom"), http.StatusInternalServerError, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			Replies{}.Reply(w, tc.value, tc.err)
			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d", w.Code, tc.status)
			}
			if tc.err == nil {
				return
			}
			var wire providerprotocol.Error
			if err := json.Unmarshal(w.Body.Bytes(), &wire); err != nil || wire.Code != tc.code || wire.Message != tc.err.Error() {
				t.Fatalf("body = %s (%v)", w.Body, err)
			}
			if ct := w.Header().Get("Content-Type"); ct != "application/json" {
				t.Fatalf("content type = %q", ct)
			}
		})
	}
}

func TestDecode(t *testing.T) {
	var v struct{ N int }
	if err := Decode(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"N":2}`)), &v); err != nil || v.N != 2 {
		t.Fatalf("Decode = %v, %+v", err, v)
	}
	err := Decode(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{broken`)), &v)
	var se StatusError
	if !errors.As(err, &se) || se.Status != http.StatusBadRequest || !strings.Contains(err.Error(), "invalid request") {
		t.Fatalf("Decode(malformed) = %v", err)
	}
}
