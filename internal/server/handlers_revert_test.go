package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestSessionRevertAndUnrevert_Post(t *testing.T) {
	var reverted *platforms.RevertSessionRequest
	var unreverted *platforms.UnrevertSessionRequest
	fake := &fakePlatform{
		id: "fake",
		revertFn: func(req platforms.RevertSessionRequest) error {
			reverted = &req
			return nil
		},
		unrevertFn: func(req platforms.UnrevertSessionRequest) error {
			unreverted = &req
			return nil
		},
	}
	srv := newForkMoveTestServer(t, fake)

	for _, tc := range []struct {
		path string
		body string
	}{
		{"/api/session/sess-1/revert", `{"messageID":"msg-9"}`},
		{"/api/session/sess-1/unrevert", ``},
	} {
		req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
		rr := httptest.NewRecorder()
		srv.dispatchSessionSubpath(rr, req)
		if rr.Code != http.StatusNoContent {
			t.Fatalf("%s status = %d, want 204; body=%s", tc.path, rr.Code, rr.Body)
		}
	}
	if reverted == nil || reverted.SessionID != "sess-1" || reverted.MessageID != "msg-9" {
		t.Fatalf("revert request = %+v, want sess-1/msg-9", reverted)
	}
	if unreverted == nil || unreverted.SessionID != "sess-1" {
		t.Fatalf("unrevert request = %+v, want sess-1", unreverted)
	}
}
