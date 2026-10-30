package codextransport

import (
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
)

func TestPlatformKeepsUnknownWriterUntouched(t *testing.T) {
	r := newRig(t)
	t.Setenv("FAKE_CODEX_OWNERSHIP_CONFLICT", "1")
	p := r.platform(t.Context())
	err := p.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: "thr", Message: "hi"})
	if !errors.Is(err, platforms.ErrSessionOwnedElsewhere) {
		t.Fatalf("lost ownership rejection: %v", err)
	}
	if _, _, err := p.ensure(t.Context()); err != nil {
		t.Fatalf("ownership rejection disrupted supervisor: %v", err)
	}
}
