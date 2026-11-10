package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/platformtest"
	"github.com/ocman/ocman/internal/server"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/statev2"
)

type unavailableCompositionNative struct{ *platformtest.Fake }

func (*unavailableCompositionNative) HistoryIdentity(context.Context) (platforms.HistoryIdentity, error) {
	return platforms.HistoryIdentity{}, errors.New("source unavailable")
}

func TestNamedCompositionRejectsMissingKeysAndDuplicateBindings(t *testing.T) {
	for _, mode := range []string{"nil-registry", "missing-key", "missing-connection", "duplicate-registry", "duplicate-router", "source-conflict", "closed-history", "unavailable-source"} {
		t.Run(mode, func(t *testing.T) {
			core := statetest.Open(t, server.ModuleSchemas()...)
			history, err := statev2.Open(filepath.Join(t.TempDir(), "history.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = history.Close() })
			store := platformconnections.New(core)
			if _, err := store.Initialize(t.Context(), platformconnections.Seed{}); err != nil {
				t.Fatal(err)
			}
			c, err := store.Create(t.Context(), platformconnections.Input{Kind: "claudecode", Name: "One", Configuration: "isolated", Directory: t.TempDir(), Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			registry := platforms.NewRegistry()
			router := platformconnections.NewRouter(store)
			keys := []platforms.ID{platforms.ID(c.ID)}
			var native platforms.Integration = &namedCompositionNative{source: "physical"}
			switch mode {
			case "nil-registry":
				registry = nil
			case "missing-key":
				keys = nil
			case "missing-connection":
				keys = []platforms.ID{"cnx_missing"}
			case "duplicate-registry":
				if err := registry.RegisterConnection(keys[0], &platformtest.Fake{PlatformID: "claudecode"}); err != nil {
					t.Fatal(err)
				}
			case "duplicate-router":
				if err := router.Bind(t.Context(), c.ID, &platformtest.Fake{PlatformID: "claudecode"}); err != nil {
					t.Fatal(err)
				}
			case "source-conflict":
				other, err := store.Create(t.Context(), platformconnections.Input{Kind: "claudecode", Name: "Other", Configuration: "isolated", Directory: t.TempDir(), Enabled: true})
				if err != nil {
					t.Fatal(err)
				}
				if err := store.BindSource(t.Context(), other.ID, "claudecode", "physical"); err != nil {
					t.Fatal(err)
				}
			case "closed-history":
				if err := history.Close(); err != nil {
					t.Fatal(err)
				}
			case "unavailable-source":
				native = &unavailableCompositionNative{&platformtest.Fake{PlatformID: "claudecode", Availability: new(false)}}
			}
			_, err = composeConnectionSessionCore(t.Context(), core, history, registry, []platforms.Integration{native}, "owner", keys, store, router)
			if mode != "unavailable-source" {
				if err == nil {
					t.Fatal("invalid composition admitted")
				}
				return
			}
			if err != nil {
				t.Fatal("offline retained composition blocked", err)
			}
			if _, _, ok := registry.PlatformForCanonicalSource(t.Context(), "other-owner", "claudecode", "physical"); ok {
				t.Fatal("cross-owner provenance resolved")
			}
			if _, err := registry.ResolveLegacyScope(t.Context(), string(keys[0])); err != nil {
				t.Fatal(err)
			}
			if _, err := registry.ResolveLegacyScope(t.Context(), "missing"); !errors.Is(err, platformconnections.ErrNotFound) {
				t.Fatal(err)
			}
		})
	}
}

func TestCanonicalPreparationCannotCompleteAgainstReadOnlyOwnerState(t *testing.T) {
	core := statetest.Open(t, server.ModuleSchemas()...)
	history, err := statev2.Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = history.Close() })
	prepare, err := composeCanonicalSessionCore(t.Context(), core, history, platforms.NewRegistry(), []platforms.Integration{&namedCompositionNative{source: "source"}}, "owner")
	if err != nil || prepare == nil {
		t.Fatal(err)
	}
	if _, err := core.SQL().ExecContext(t.Context(), "PRAGMA query_only=ON"); err != nil {
		t.Fatal(err)
	}
	if err := prepare(t.Context()); err == nil {
		t.Fatal("read-only cutover reported complete")
	}
}
