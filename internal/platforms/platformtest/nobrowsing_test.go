package platformtest

import (
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
)

func TestNoBrowsingFailsEveryPersistedRead(t *testing.T) {
	var b platforms.Browsing = NoBrowsing{}
	ctx := t.Context()
	reads := map[string]func() error{
		"session":  func() error { _, err := b.Session(ctx, "s", 1, 0); return err },
		"inactive": func() error { _, err := b.SessionsInactiveBefore(ctx, 0); return err },
		"changes":  func() error { _, err := b.SessionChanges(ctx, "s"); return err },
		"info":     func() error { _, err := b.SessionInfo(ctx, "s"); return err },
		"agents":   func() error { _, err := b.AgentCatalog(ctx, "s"); return err },
		"commands": func() error { _, err := b.SlashCommands(ctx, "s"); return err },
		"models":   func() error { _, err := b.SessionModels(ctx, "s"); return err },
		"catalog": func() error {
			_, err := b.DirectoryCatalog(ctx, platforms.DirectoryCatalogRequest{})
			return err
		},
	}
	for name, read := range reads {
		if err := read(); !errors.Is(err, platforms.ErrHistoryUnavailable) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if b.Owns(ctx, "s") {
		t.Error("owns a session without history")
	}
}
