package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/server"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/statev2"
)

type namedCompositionNative struct {
	appCanonicalNative
	source string
}

func (*namedCompositionNative) ID() platforms.ID                     { return "claudecode" }
func (*namedCompositionNative) DisplayName() string                  { return "Claude Code" }
func (*namedCompositionNative) Available(context.Context) bool       { return true }
func (*namedCompositionNative) Capabilities() platforms.Capabilities { return platforms.Capabilities{} }
func (p *namedCompositionNative) HistoryIdentity(context.Context) (platforms.HistoryIdentity, error) {
	return platforms.HistoryIdentity{Platform: "claudecode", SourceKey: p.source}, nil
}

func TestSavedNamedCompositionSeparatesSameKindAndRetainsRemovedReads(t *testing.T) {
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
	var keys []platforms.ID
	var native []platforms.Integration
	for _, name := range []string{"one", "two"} {
		c, err := store.Create(t.Context(), platformconnections.Input{Kind: "claudecode", Name: name, Configuration: "isolated", Directory: filepath.Join(t.TempDir(), name), Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, platforms.ID(c.ID))
		native = append(native, &namedCompositionNative{source: name})
	}
	registry := platforms.NewRegistry()
	router := platformconnections.NewRouter(store)
	prepare, err := composeConnectionSessionCore(t.Context(), core, history, registry, native, "owner", keys, store, router)
	if err != nil {
		t.Fatal(err)
	}
	if prepare != nil {
		if err := prepare(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	catalogue, err := sessioncatalog.New(core)
	if err != nil {
		t.Fatal(err)
	}
	for i, source := range []string{"one", "two"} {
		row, err := catalogue.FindBinding(t.Context(), sessioncatalog.Binding{OwnerID: "owner", Platform: "claudecode", SourceKey: source, NativeID: "native"})
		if err != nil {
			t.Fatal(err)
		}
		if err := history.RegisterInstance(t.Context(), statev2.Instance{ID: source, OwnerID: "owner", Platform: "claudecode", SourceKey: source}); err != nil {
			t.Fatal(err)
		}
		if _, err := history.Commit(t.Context(), statev2.Batch{Session: statev2.Session{Key: statev2.Key{InstanceID: source, SessionID: row.ID}, Title: source}}); err != nil {
			t.Fatal(err)
		}
		p, ok := registry.Get(keys[i])
		if !ok || p.ID() != "claudecode" {
			t.Fatal("lost named key/native kind")
		}
		rows, err := p.Sessions(t.Context(), "", 0)
		if err != nil || len(rows) != 1 || rows[0].ID != row.ID {
			t.Fatalf("same-kind crossover: %+v %v", rows, err)
		}
	}
	if err := store.Remove(t.Context(), string(keys[0])); err != nil {
		t.Fatal(err)
	}
	restarted := platforms.NewRegistry()
	retainedRouter := platformconnections.NewRouter(store)
	if err := registerRetainedConnections(t.Context(), core, history, restarted, retainedRouter, store, "owner"); err != nil {
		t.Fatal(err)
	}
	p, ok := restarted.Get(keys[0])
	if !ok {
		t.Fatal("removed connection lost retained adapter")
	}
	if rows, err := p.Sessions(t.Context(), "", 0); err != nil || len(rows) != 1 {
		t.Fatalf("lost retained read: %+v %v", rows, err)
	}
	if _, err := retainedRouter.Begin(t.Context(), string(keys[0])); err == nil {
		t.Fatal("removed connection admitted mutation")
	}
}
