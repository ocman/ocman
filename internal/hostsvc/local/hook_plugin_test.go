package local

import (
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
)

func TestHookPluginWithoutManagerIsUnavailable(t *testing.T) {
	for _, deps := range []Deps{{}, {HookPlugin: func() hostsvc.HookPluginManager { return nil }}} {
		h := New(deps)
		if available, installed := h.HookPlugin(); available || installed {
			t.Fatal("missing hook manager reported as available")
		}
		if err := h.InstallHookPlugin(t.Context()); !errors.Is(err, platforms.ErrUnsupported) {
			t.Fatalf("install = %v", err)
		}
		if err := h.UninstallHookPlugin(t.Context()); !errors.Is(err, platforms.ErrUnsupported) {
			t.Fatalf("uninstall = %v", err)
		}
	}
}
