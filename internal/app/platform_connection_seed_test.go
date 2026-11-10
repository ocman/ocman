package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/server"
	"github.com/ocman/ocman/internal/state"
)

func TestConnectionSeedUsesPreOpenSnapshotAndNeverReseeds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	existed, err := existingInstallation(path)
	if err != nil || existed {
		t.Fatalf("fresh snapshot: %v %v", existed, err)
	}
	db, err := state.Open(path, server.ModuleSchemas()...)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := initializeConnections(t.Context(), db, existed, map[string]bool{"claudecode": true}, connectionConfigurations())
	if err != nil {
		t.Fatal(err)
	}
	collection, err := store.Read(t.Context())
	if err != nil || !collection.Initialized || collection.OnboardingComplete || len(collection.Connections) != 0 {
		t.Fatalf("fresh: %+v %v", collection, err)
	}
	if _, err := initializeConnections(t.Context(), db, true, map[string]bool{"claudecode": true}, connectionConfigurations()); err != nil {
		t.Fatal(err)
	}
	collection, err = store.Read(t.Context())
	if err != nil || len(collection.Connections) != 0 || collection.OnboardingComplete {
		t.Fatalf("reseeded: %+v %v", collection, err)
	}
}

func TestMigratedSeedKeepsDisabledReferencesAndEffectiveOriginalDefault(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"), server.ModuleSchemas()...)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.PinSession(t.Context(), "opencode", "old-native"); err != nil {
		t.Fatal(err)
	}
	store, err := initializeConnections(t.Context(), db, true, map[string]bool{"claudecode": true}, connectionConfigurations())
	if err != nil {
		t.Fatal(err)
	}
	claude, err := store.Legacy(t.Context(), "claudecode")
	if err != nil {
		t.Fatal(err)
	}
	openCode, err := store.Legacy(t.Context(), "opencode")
	if err != nil || openCode.Enabled {
		t.Fatalf("disabled original reference lost: %+v %v", openCode, err)
	}
	def, err := store.Default(t.Context())
	if err != nil || def.ID != claude.ID {
		t.Fatalf("migration changed effective default: %+v %v", def, err)
	}
	if err := store.Remove(t.Context(), claude.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := initializeConnections(t.Context(), db, true, map[string]bool{"opencode": true}, connectionConfigurations()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Default(t.Context()); !errors.Is(err, platformconnections.ErrUnavailable) {
		t.Fatal("removed default switched account", err)
	}
}

func TestConnectionAuthSurvivesCompositionRestart(t *testing.T) {
	dir := t.TempDir()
	db, err := state.Open(filepath.Join(dir, "state.db"), platformconnections.Schema)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	f := connectionFactory{state: db, dataDir: dir}
	c := platformconnections.Connection{ID: "cnx_account", Kind: "opencode"}
	a, err := f.connectionAuth(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.connectionAuth(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	if !a.Enabled() || a != b {
		t.Fatal("connection auth rotated across restart")
	}
	c.ID = "cnx_other"
	other, err := f.connectionAuth(t.Context(), c)
	if err != nil || a == other {
		t.Fatal("connections shared auth", err)
	}
}

func TestExistingInstallationRejectsMissingOrEmptyState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if existed, err := existingInstallation(path); err != nil || existed {
		t.Fatalf("empty state: %v %v", existed, err)
	}
	if err := os.WriteFile(path, []byte("preexisting"), 0600); err != nil {
		t.Fatal(err)
	}
	if existed, err := existingInstallation(path); err != nil || !existed {
		t.Fatalf("preexisting state: %v %v", existed, err)
	}
}
