package httpx

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
)

func TestBusyErrorShowsRecoveryInstructions(t *testing.T) {
	const recovery = "Another Codex process owns this thread. Close the session in that process and retry."
	for _, tt := range []struct {
		err  error
		want string
	}{
		{&platforms.BusyError{Message: recovery, Cause: errors.New("internal RPC -32600")}, recovery},
		{&platforms.BusyError{}, "session is currently processing a prompt; try again in a moment"},
		{platforms.ErrBusy, "session is currently processing a prompt; try again in a moment"},
	} {
		w := httptest.NewRecorder()
		WritePlatformError(w, "sending message", fmt.Errorf("send: %w", tt.err))
		if w.Code != http.StatusConflict || strings.TrimSpace(w.Body.String()) != tt.want {
			t.Fatalf("response = %d %q, want 409 %q", w.Code, w.Body.String(), tt.want)
		}
	}
}
