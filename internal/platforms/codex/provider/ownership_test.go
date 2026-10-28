package codexprovider

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
	"github.com/ocman/ocman/internal/platforms/codex/appserver/codextest"
	"github.com/ocman/ocman/internal/providerprotocol"
)

func TestThreadLoadError(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want int
	}{
		{"writer", &codexappserver.RPCError{Code: -32600, Message: "thread thr already has an active writer"}, http.StatusConflict},
		{"missing", &codexappserver.RPCError{Code: -32600, Message: "thread not loaded: thr"}, http.StatusNotFound},
		{"other rejection", &codexappserver.RPCError{Code: -32600, Message: "invalid request"}, http.StatusInternalServerError},
		{"other code", &codexappserver.RPCError{Code: -32603, Message: "already has an active writer"}, http.StatusInternalServerError},
		{"transport", errors.New("already has an active writer"), http.StatusInternalServerError},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := threadLoadError(tt.err)
			if errStatus(got) != tt.want {
				t.Fatalf("threadLoadError = %v (%d), want status %d", got, errStatus(got), tt.want)
			}
			if tt.want != http.StatusNotFound && !errors.Is(got, tt.err) {
				t.Fatalf("threadLoadError lost original cause: %v", got)
			}
		})
	}
}

func TestSendThreadOwnedElsewhere(t *testing.T) {
	h := newHarness(t, func(s *codextest.Server) {
		scripted(s)
		s.Handle(codexappserver.MethodThreadResume, func(json.RawMessage) (any, error) {
			return nil, &codexappserver.RPCError{Code: -32600, Message: "thread thr already has an active writer"}
		})
	})
	var got providerprotocol.Error
	code := h.call(http.MethodPost, "/sessions/thr/send", platforms.SendMessageRequest{Message: "hi"}, &got)
	if code != http.StatusConflict {
		t.Fatalf("send = %d: %s; want ownership conflict, not HTTP 500", code, got.Message)
	}
	if !strings.Contains(got.Message, "Close") || !strings.Contains(got.Message, "retry") {
		t.Fatalf("ownership conflict gives no recovery instructions: %s", got.Message)
	}
	if strings.Contains(got.Message, "-32600") || strings.Contains(got.Message, "platforms:") {
		t.Fatalf("ownership conflict exposes internal error details: %s", got.Message)
	}
	for _, call := range h.fake().Calls() {
		if call.Method == codexappserver.MethodTurnStart {
			t.Fatal("started a turn without thread ownership")
		}
	}
	if h.b.sup.Active("thr") {
		t.Fatal("ownership conflict admitted a turn")
	}
	// Releasing the other writer must allow the next send to resume again.
	scripted(h.fake())
	h.start("thr")
}
