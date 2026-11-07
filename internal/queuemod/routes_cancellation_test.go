package queuemod

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type failingQueueRead struct {
	Store
	err error
}

func (s failingQueueRead) ListQueuedMessages(context.Context, string, string) ([]state.QueuedMessage, error) {
	return nil, s.err
}

func (s failingQueueRead) ListQueuedMessagesAnyPlatform(context.Context, string) ([]state.QueuedMessage, error) {
	return nil, s.err
}

func TestQueueListCancellation(t *testing.T) {
	for _, platform := range []string{"", "?platform=opencode"} {
		for _, tc := range []struct {
			name string
			err  error
			code int
		}{
			{"canceled", fmt.Errorf("listing queued messages: %w", context.Canceled), 499},
			{"grpc canceled", status.Error(codes.Canceled, "request canceled"), 499},
			{"deadline", context.DeadlineExceeded, 500},
			{"storage", errors.New("storage unavailable"), 500},
		} {
			t.Run(tc.name+platform, func(t *testing.T) {
				m := NewModule(Deps{Store: failingQueueRead{err: tc.err}})
				r := httptest.NewRequest(http.MethodGet, "/api/session/s1/queue"+platform, nil)
				r.SetPathValue("id", "s1")
				w := httptest.NewRecorder()
				m.List(w, r)
				if w.Code != tc.code {
					t.Fatalf("status=%d body=%s, want %d", w.Code, w.Body.String(), tc.code)
				}
			})
		}
	}
}

func TestHeldQueueDoesNotProbeNativeSession(t *testing.T) {
	m := NewModule(Deps{Store: statetest.Open(t), Adapter: func(context.Context, string, string) (platforms.Platform, bool) {
		t.Fatal("draft queue contacted a platform before binding")
		return nil, false
	}})
	r := httptest.NewRequest(http.MethodGet, "/api/session-drafts/s1/queue?platform=r-box:opencode", nil)
	r.SetPathValue("id", "s1")
	w := httptest.NewRecorder()
	m.ListHeld(w, r)
	if w.Code != http.StatusOK || w.Body.String() != "[]\n" {
		t.Fatalf("empty held queue: %d %s", w.Code, w.Body.String())
	}
	r.SetPathValue("qmid", "msg_native")
	w = httptest.NewRecorder()
	m.DeleteHeld(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("native ID was not a held-queue no-op: %d %s", w.Code, w.Body.String())
	}
}
