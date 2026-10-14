package provider

import (
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestGuardedTitleUsesDedicatedRouteAndCapability(t *testing.T) {
	caps := allCapabilities()
	caps.AutoTitle = true
	f := newFake(t, caps)
	f.reply("POST", "/sessions/s/auto-title", nil)
	a := f.adapter(t)
	if err := a.RenameSession(t.Context(), platforms.RenameSessionRequest{SessionID: "s", Title: "Generated", OnlyIfUntitled: true}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	if len(f.calls) == 0 || f.calls[len(f.calls)-1].path != "/sessions/s/auto-title" {
		t.Fatal(f.calls)
	}
	f.mu.Unlock()
	old := newFake(t, allCapabilities()).adapter(t)
	if err := old.RenameSession(t.Context(), platforms.RenameSessionRequest{SessionID: "s", Title: "Generated", OnlyIfUntitled: true}); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatalf("old provider accepted a guarded title: %v", err)
	}
}
