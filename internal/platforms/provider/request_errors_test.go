package provider

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

func TestSendConflictMessageReachesBrowser(t *testing.T) {
	const recovery = "Another Codex process owns this thread. Close the session in that process and retry."
	for _, message := range []string{recovery, ""} {
		t.Run(message, func(t *testing.T) {
			f := newFake(t, allCapabilities())
			f.replies["POST /sessions/s/send"] = fakeReply{http.StatusConflict, providerprotocol.Error{Message: message}}
			a := f.adapter(t)
			err := a.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: "s"})
			if !errors.Is(err, platforms.ErrBusy) {
				t.Fatalf("send = %v, want ErrBusy", err)
			}
			rr := httptest.NewRecorder()
			httpx.WritePlatformError(rr, "sending message", err)
			want := message
			if want == "" {
				want = "session is currently processing a prompt; try again in a moment"
			}
			if rr.Code != http.StatusConflict || strings.TrimSpace(rr.Body.String()) != want {
				t.Fatalf("browser response = %d %q, want 409 %q", rr.Code, rr.Body.String(), want)
			}
		})
	}
}

func TestOnlyExplicitOwnershipRejectionsAllowRecovery(t *testing.T) {
	for _, status := range []int{http.StatusConflict, http.StatusInternalServerError} {
		err := providerResponseError(status, providerprotocol.Error{Code: providerprotocol.CodeSessionOwnedElsewhere, Message: "owned elsewhere"})
		if errors.Is(err, platforms.ErrSessionOwnedElsewhere) != (status == http.StatusConflict) {
			t.Fatalf("status %d incorrectly classified for recovery: %v", status, err)
		}
	}
}
