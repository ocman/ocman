package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/autoapprove"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/identity"
	"github.com/NoUseFreak/ocman/internal/platforms/owned"
	"github.com/NoUseFreak/ocman/internal/pluginsmod"
	"github.com/NoUseFreak/ocman/internal/server"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

// Tests drive scheduled cutover synchronously without launching a server.
func prepareCanonicalSessionCore(ctx context.Context, core *state.DB, history *statev2.DB, registry *platforms.Registry, native []platforms.Integration, owner string) error {
	work, err := composeCanonicalSessionCore(ctx, core, history, registry, native, owner)
	if err != nil || work == nil {
		return err
	}
	return work(ctx)
}

func TestCanonicalStartupCancellationIsGracefulOnlyForStoppedComposition(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := canonicalStartupError(ctx, context.Canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("live composition swallowed source error: %v", err)
	}
	cancel()
	if err := canonicalStartupError(ctx, context.Canceled); err != nil {
		t.Fatalf("normal shutdown became startup failure: %v", err)
	}
	if err := canonicalStartupError(ctx, sessioncatalog.ErrBindingConflict); !errors.Is(err, sessioncatalog.ErrBindingConflict) {
		t.Fatalf("shutdown swallowed migration conflict: %v", err)
	}
}

type appCanonicalNative struct{ platforms.Platform }

func (*appCanonicalNative) ID() platforms.ID { return "test" }
func (*appCanonicalNative) HistoryIdentity(context.Context) (platforms.HistoryIdentity, error) {
	return platforms.HistoryIdentity{Platform: "test", SourceKey: "source"}, nil
}
func (*appCanonicalNative) Sessions(context.Context, string, int64) ([]db.Session, error) {
	return []db.Session{{ID: "native", Directory: "/repo"}}, nil
}
func (*appCanonicalNative) SessionIdentityMetadata(_ context.Context, id string) (*db.Session, error) {
	return &db.Session{ID: id, Directory: "/repo"}, nil
}
func (*appCanonicalNative) PermissionJudge() autoapprove.Judge {
	return autoapprove.ClaudeJudge{CLI: "not-executed"}
}

func (*appCanonicalNative) LiveStatus(string) *platforms.LiveState { return nil }

func TestPreServingCompositionMigratesPluginsBeforeEnablingCanonicalRegistry(t *testing.T) {
	core := statetest.Open(t, server.ModuleSchemas()...)
	history, err := statev2.Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = history.Close() })
	plugins := pluginsmod.NewStore(core)
	if err := plugins.LinkPluginSession(t.Context(), "test", "native", "plugin"); err != nil {
		t.Fatal(err)
	}
	registry := platforms.NewRegistry()
	if err := prepareCanonicalSessionCore(t.Context(), core, history, registry, []platforms.Integration{&appCanonicalNative{}}, "owner"); err != nil {
		t.Fatal(err)
	}
	if !registry.CanonicalSessionIDs() {
		t.Fatal("successful migration did not enforce canonical registry")
	}
	adapter, _ := registry.Get("test")
	if rows, err := adapter.Sessions(t.Context(), "", 0); err != nil || len(rows) != 0 {
		t.Fatalf("pre-ingestion read fell back to native inventory: %+v %v", rows, err)
	}
	catalogue, err := sessioncatalog.New(core)
	if err != nil {
		t.Fatal(err)
	}
	identityRow, err := catalogue.FindBinding(t.Context(), sessioncatalog.Binding{OwnerID: "owner", Platform: "test", SourceKey: "source", NativeID: "native"})
	if err != nil {
		t.Fatal(err)
	}
	if err := history.RegisterInstance(t.Context(), statev2.Instance{ID: "source", OwnerID: "owner", Platform: "test", SourceKey: "source"}); err != nil {
		t.Fatal(err)
	}
	if _, err := history.Commit(t.Context(), statev2.Batch{Session: statev2.Session{Key: statev2.Key{InstanceID: "source", SessionID: identityRow.ID}, Directory: "/repo"}}); err != nil {
		t.Fatal(err)
	}
	rows, err := adapter.Sessions(t.Context(), "", 0)
	if err != nil || len(rows) != 1 || !identity.IsSessionID(rows[0].ID) {
		t.Fatalf("native inventory escaped prepared registry: %+v %v", rows, err)
	}
	if _, ok := registry.PlatformForSession(t.Context(), "native"); ok {
		t.Fatal("prepared registry routed native caller reference")
	}
	if plugin, linked, err := plugins.GetPluginSessionPlugin(t.Context(), "test", rows[0].ID); err != nil || !linked || plugin != "plugin" {
		t.Fatalf("registry enabled before plugin migration: %q %v %v", plugin, linked, err)
	}
	if provider, ok := adapter.(interface{ PermissionJudge() autoapprove.Judge }); !ok || provider.PermissionJudge() == nil {
		t.Fatal("canonical composition lost fallback judge")
	}
	catalogue, err = sessioncatalog.New(core)
	if err != nil {
		t.Fatal(err)
	}
	gateway := owned.NewIdentityGateway(catalogue, "owner", func() owned.IdentityRegistry { return registry }, func(_ context.Context, scope string) (platforms.Platform, error) {
		p, _ := registry.Get(platforms.ID(scope))
		return p, nil
	}, func(string) error { return sessioncatalog.ErrInvalid })
	if info, err := gateway.Lookup(t.Context(), "test", platforms.SessionIdentityRequest{NativeID: "late-native"}); err != nil || !identity.IsSessionID(info.ID) || info.NativeID != "" {
		t.Fatalf("judge decoration broke legacy-link adoption: %+v %v", info, err)
	}
}
