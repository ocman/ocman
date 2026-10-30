package codexprovider

import (
	"errors"
	"testing"

	acp "github.com/coder/acp-go-sdk"
	"github.com/ocman/ocman/internal/platforms"
)

func TestACPOwnershipRejection(t *testing.T) {
	err := threadLoadError(&acp.RequestError{Code: -32600, Message: "thread thr already has an active writer"})
	if !errors.Is(err, platforms.ErrSessionOwnedElsewhere) || !errors.Is(err, platforms.ErrBusy) {
		t.Fatal(err)
	}
	other := errors.New("connection failed")
	if !errors.Is(threadLoadError(other), other) {
		t.Fatal("unrelated error became ownership proof")
	}
	t.Setenv("FAKE_CODEX_OWNERSHIP_CONFLICT", "1")
	h, _ := acpHarness(t)
	var response struct {
		Code string `json:"code"`
	}
	if code := h.call("POST", "/sessions/thr/send", platforms.SendMessageRequest{Message: "hi"}, &response); code != 409 || response.Code != "session_owned_elsewhere" {
		t.Fatalf("ownership response = %d %+v", code, response)
	}
}
