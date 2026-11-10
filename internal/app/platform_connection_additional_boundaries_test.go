package app

import (
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/ocruntime"
	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
	ocapi "github.com/ocman/ocman/internal/platforms/opencode/api"
	"github.com/ocman/ocman/internal/platforms/platformtest"
	"github.com/ocman/ocman/internal/server"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/statev2"
)

func TestUnsavedConnectionInspectionHandlesMissingNativeDependencies(t *testing.T) {
	// Missing CLIs must be reported without ever executing a user's native CLI.
	t.Setenv("PATH", t.TempDir())
	core := statetest.Open(t, platformconnections.Schema)
	factory := connectionFactory{state: core, dataDir: t.TempDir()}
	lifecycle := connectionLifecycle(factory, platformconnections.NewRouter(platformconnections.New(core)), nil, nil)
	for _, kind := range []string{"claudecode", "codex"} {
		check, err := lifecycle.Inspect(t.Context(), platformconnections.Connection{ID: "cnx_unsaved", Kind: kind, Directory: t.TempDir()})
		if err != nil || check.Status != "sign-in-required" || check.Command == "" {
			t.Fatalf("missing native dependency: %+v %v", check, err)
		}
	}
	bare := &platformtest.Fake{PlatformID: "claudecode"}
	lifecycle = connectionLifecycle(factory, platformconnections.NewRouter(platformconnections.New(core)), []platforms.ID{"cnx_bare"}, []platforms.Integration{bare})
	if check, err := lifecycle.Inspect(t.Context(), platformconnections.Connection{ID: "cnx_bare", Kind: "claudecode"}); err != nil || check.Status != "unchecked" {
		t.Fatal(check, err)
	}
	if err := core.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := lifecycle.Inspect(t.Context(), platformconnections.Connection{ID: "cnx_unsaved", Kind: "opencode", Directory: t.TempDir()}); err == nil {
		t.Fatal("OpenCode inspection ignored secret store failure")
	}
}

func TestLegacyOpenCodeFactoryOwnsOnlyItsReadOnlyDatabase(t *testing.T) {
	core := statetest.Open(t, platformconnections.Schema)
	store := platformconnections.New(core)
	if _, err := store.Initialize(t.Context(), platformconnections.Seed{ExistingInstallation: true, Connections: []platformconnections.Input{{Kind: "opencode", Name: "Original", Configuration: "existing", Directory: t.TempDir(), Enabled: true}}, DefaultKind: "opencode"}); err != nil {
		t.Fatal(err)
	}
	c, err := store.Legacy(t.Context(), "opencode")
	if err != nil {
		t.Fatal(err)
	}
	nativePath := filepath.Join(t.TempDir(), "native.db")
	native, err := db.OpenReadWrite(nativePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := native.Close(); err != nil {
		t.Fatal(err)
	}
	factory := connectionFactory{state: core, dataDir: t.TempDir(), openCodeDB: nativePath, runtimeBuilder: func(ocruntime.NativeConnectionConfig, ocapi.Auth) (ocruntime.Runtime, error) {
		return &factoryRuntime{}, nil
	}}
	p, err := factory.openCode(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	p.(*openCodeConnection).Close()
	// Closing the composed read-only pool does not remove or mutate native data.
	if reopened, err := db.Open(nativePath); err != nil {
		t.Fatal(err)
	} else if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	factory.openCodeDB = filepath.Join(t.TempDir(), "missing.db")
	if _, err := factory.openCode(t.Context(), c); err == nil {
		t.Fatal("missing legacy native database published an adapter")
	}
}

func TestFactoryRejectsNativeConfigurationAndSecretWriteFailures(t *testing.T) {
	core := statetest.Open(t, platformconnections.Schema)
	factory := connectionFactory{state: core, dataDir: t.TempDir()}
	c := platformconnections.Connection{ID: "cnx_unsaved", Kind: "opencode", Directory: "relative"}
	if _, err := factory.openCode(t.Context(), c); err == nil {
		t.Fatal("invalid explicit configuration reached a launch")
	}
	if _, err := factory.connectionAuth(t.Context(), platformconnections.Connection{ID: "cnx_bootstrap", Kind: "opencode"}); err != nil {
		t.Fatal(err)
	}
	if _, err := core.SQL().ExecContext(t.Context(), "PRAGMA query_only=ON"); err != nil {
		t.Fatal(err)
	}
	if _, err := factory.connectionAuth(t.Context(), platformconnections.Connection{ID: "cnx_new", Kind: "opencode"}); err == nil {
		t.Fatal("failed durable private password was ignored")
	}
}

func TestLegacySourceImportIsReplayableAndFailsClosedOnUnavailableStores(t *testing.T) {
	core := statetest.Open(t, server.ModuleSchemas()...)
	store := platformconnections.New(core)
	if _, err := store.Initialize(t.Context(), platformconnections.Seed{ExistingInstallation: true, Connections: []platformconnections.Input{{Kind: "claudecode", Name: "Original", Configuration: "existing", Directory: t.TempDir(), Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
	history, err := statev2.Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = history.Close() })
	if err := history.RegisterInstance(t.Context(), statev2.Instance{ID: "old", OwnerID: "owner", Platform: "claudecode", SourceKey: "old-source"}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := bindMigratedHistorySources(t.Context(), store, history, "owner"); err != nil {
			t.Fatal("replayed source import changed ownership", err)
		}
	}
	if err := history.Close(); err != nil {
		t.Fatal(err)
	}
	if err := bindMigratedHistorySources(t.Context(), store, history, "owner"); err == nil {
		t.Fatal("unreadable history was silently assigned")
	}
	if err := core.Close(); err != nil {
		t.Fatal(err)
	}
	if err := bindMigratedHistorySources(t.Context(), store, history, "owner"); err == nil {
		t.Fatal("unreadable collection was silently ignored")
	}
	if err := registerRetainedConnections(t.Context(), core, history, platforms.NewRegistry(), platformconnections.NewRouter(store), store, "owner"); err == nil {
		t.Fatal("unreadable source mappings published retained adapters")
	}
}
