package provider

import (
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

func TestSessionOwnershipCapability(t *testing.T) {
	for _, supported := range []bool{false, true} {
		caps := allCapabilities()
		caps.Ownership = supported
		f := newFake(t, caps)
		f.reply("GET", "/sessions/s/ownership", providerprotocol.SessionOwnership{SourceKey: "/codex", Loaded: true, Idle: true})
		a := f.adapter(t)
		owned, err := a.SessionOwnership(t.Context(), "s")
		if supported {
			if err != nil || !owned.Loaded || !owned.Idle || owned.SourceKey != "/codex" {
				t.Fatalf("ownership = %+v, %v", owned, err)
			}
		} else if !errors.Is(err, platforms.ErrUnsupported) {
			t.Fatalf("unsupported probe = %v", err)
		}
	}
}
