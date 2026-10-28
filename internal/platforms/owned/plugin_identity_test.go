package owned

import (
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

func TestPluginCompatibilityKeepsLegacyWireIDsWithoutGrantingExecution(t *testing.T) {
	a, p, catalogue := fixture(t)
	publish(t, a, p.rows...)
	rows, err := a.Sessions(t.Context(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	id := rows[0].ID
	if got, err := a.PluginSessionReference(t.Context(), id); err != nil || got != id {
		t.Fatalf("new session used native wire correlation: %q %v", got, err)
	}
	if err := catalogue.RetainLegacyKeys(t.Context(), "owner", id); err != nil {
		t.Fatal(err)
	}
	if got, err := a.PluginSessionReference(t.Context(), id); err != nil || got != "native" {
		t.Fatalf("legacy wire correlation lost: %q %v", got, err)
	}
	p.source = "replacement-source"
	if got, err := a.PluginSessionReference(t.Context(), id); err != nil || got != "native" {
		t.Fatalf("inactive source lost already owed wire delivery: %q %v", got, err)
	}
	if _, err := a.nativeID(t.Context(), id); !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatalf("wire compatibility granted inactive execution: %v", err)
	}
	if _, err := a.PluginSessionReference(t.Context(), "native"); !errors.Is(err, sessioncatalog.ErrInvalid) {
		t.Fatalf("compatibility accepted native caller ID: %v", err)
	}
}
