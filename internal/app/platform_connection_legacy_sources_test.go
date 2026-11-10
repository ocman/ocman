package app

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/server"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/statev2"
)

func TestMigratedLegacyRetainsProvenSourceSetWithoutAcquiringNewAccountSources(t *testing.T) {
	core := statetest.Open(t, server.ModuleSchemas()...)
	history, err := statev2.Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = history.Close() })
	catalogue, err := sessioncatalog.New(core)
	if err != nil {
		t.Fatal(err)
	}
	store := platformconnections.New(core)
	if _, err := store.Initialize(t.Context(), platformconnections.Seed{ExistingInstallation: true, Connections: []platformconnections.Input{{Kind: "claudecode", Name: "Machine", Configuration: "existing", Directory: "/active", Enabled: false}}, DefaultKind: "claudecode"}); err != nil {
		t.Fatal(err)
	}
	c, err := store.Legacy(t.Context(), "claudecode")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, source := range []string{"old-one", "old-two"} {
		if _, err := catalogue.Reconcile(t.Context(), sessioncatalog.Source{OwnerID: "owner", Platform: "claudecode", SourceKey: source}, []sessioncatalog.NativeSession{{ID: "same-native", Directory: "/repo"}}); err != nil {
			t.Fatal(err)
		}
		row, err := catalogue.FindBinding(t.Context(), sessioncatalog.Binding{OwnerID: "owner", Platform: "claudecode", SourceKey: source, NativeID: "same-native"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, row.ID)
		if err := history.RegisterInstance(t.Context(), statev2.Instance{ID: source, OwnerID: "owner", Platform: "claudecode", SourceKey: source}); err != nil {
			t.Fatal(err)
		}
		if _, err := history.Commit(t.Context(), statev2.Batch{Session: statev2.Session{Key: statev2.Key{InstanceID: source, SessionID: row.ID}, Title: source, Directory: "/repo"}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := bindMigratedHistorySources(t.Context(), store, history, "owner"); err != nil {
		t.Fatal(err)
	}
	registry := platforms.NewRegistry()
	router := platformconnections.NewRouter(store)
	if err := registerRetainedConnections(t.Context(), core, history, registry, router, store, "owner"); err != nil {
		t.Fatal(err)
	}
	if len(registry.Entries()) != 1 {
		t.Fatal("registered duplicate adapter per physical source")
	}
	p, _ := registry.Get(platforms.ID(c.ID))
	rows, err := p.Sessions(t.Context(), "", 0)
	if err != nil || len(rows) != 2 {
		t.Fatalf("lost retained source set: %+v %v", rows, err)
	}
	for _, id := range ids {
		if _, err := p.(platforms.SessionMetadataReader).SessionMetadata(t.Context(), id); err != nil {
			t.Fatal("old identity rejected", err)
		}
	}
	if _, err := router.Begin(t.Context(), c.ID); !errors.Is(err, platformconnections.ErrUnavailable) {
		t.Fatal("disabled legacy mutation admitted", err)
	}
	if _, err := store.Create(t.Context(), platformconnections.Input{Kind: "claudecode", Name: "New account", Configuration: "isolated", Directory: t.TempDir(), Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := history.RegisterInstance(t.Context(), statev2.Instance{ID: "unknown", OwnerID: "owner", Platform: "claudecode", SourceKey: "unknown"}); err != nil {
		t.Fatal(err)
	}
	if err := bindMigratedHistorySources(t.Context(), store, history, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SourceConnection(t.Context(), "claudecode", "unknown"); !errors.Is(err, platformconnections.ErrNotFound) {
		t.Fatal("unknown acquired original account", err)
	}
	if err := store.Remove(t.Context(), c.ID); err != nil {
		t.Fatal(err)
	}
	restarted := platforms.NewRegistry()
	if err := registerRetainedConnections(t.Context(), core, history, restarted, platformconnections.NewRouter(store), store, "owner"); err != nil {
		t.Fatal(err)
	}
	p, _ = restarted.Get(platforms.ID(c.ID))
	if rows, err := p.Sessions(t.Context(), "", 0); err != nil || len(rows) != 2 {
		t.Fatalf("removed source set lost on restart: %+v %v", rows, err)
	}
}
