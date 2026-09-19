package state

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/plugins"
)

func TestPluginOperationSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	d, err := Open(path)
	requirePluginOK(t, err)
	requirePluginOK(t, d.ReservePluginOperation(t.Context(), "org.example.test", "operation"))
	requirePluginOK(t, d.Close())
	d, err = Open(path)
	requirePluginOK(t, err)
	t.Cleanup(func() { _ = d.Close() })
	var wire *plugins.WireError
	if err := d.ReservePluginOperation(t.Context(), "org.example.test", "operation"); !errors.As(err, &wire) || wire.Category != plugins.ErrorConflict {
		t.Fatalf("operation replayed: %v", err)
	}
	requirePluginOK(t, d.ReservePluginOperation(t.Context(), "org.example.other", "operation"))
	requirePluginOK(t, d.Close())
	if err := d.ReservePluginOperation(t.Context(), "org.example.test", "new"); !errors.Is(err, ErrPluginState) {
		t.Fatal(err)
	}
}
