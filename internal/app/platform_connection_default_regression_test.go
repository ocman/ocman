package app

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/server"
	"github.com/ocman/ocman/internal/state"
)

func TestMigrationPreservesExplicitDisabledDefaultAcrossRestartAndEnable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	core, err := state.Open(path, server.ModuleSchemas()...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = core.Close() }()
	if err := core.SetSetting(t.Context(), state.DefaultPlatformSetting, "codex"); err != nil {
		t.Fatal(err)
	}
	store, err := initializeConnections(t.Context(), core, true, map[string]bool{"claudecode": true}, connectionConfigurations())
	if err != nil {
		t.Fatal(err)
	}
	codex, err := store.Legacy(t.Context(), "codex")
	if err != nil {
		t.Fatalf("saved disabled preference was omitted: %v", err)
	}
	if codex.Enabled {
		t.Fatal("migration enabled saved disabled connection")
	}
	collection, err := store.Read(t.Context())
	if err != nil || collection.DefaultID != codex.ID {
		t.Fatalf("saved default replaced: %+v %v", collection, err)
	}
	if _, err := store.Default(t.Context()); !errors.Is(err, platformconnections.ErrUnavailable) {
		t.Fatal("disabled preference fell back", err)
	}
	if err := core.Close(); err != nil {
		t.Fatal(err)
	}
	core, err = state.Open(path, server.ModuleSchemas()...)
	if err != nil {
		t.Fatal(err)
	}
	store, err = initializeConnections(t.Context(), core, true, map[string]bool{"claudecode": true}, connectionConfigurations())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetEnabled(t.Context(), codex.ID, true); err != nil {
		t.Fatal(err)
	}
	def, err := store.Default(t.Context())
	if err != nil || def.ID != codex.ID {
		t.Fatalf("enabling original default did not restore saved target: %+v %v", def, err)
	}
}
