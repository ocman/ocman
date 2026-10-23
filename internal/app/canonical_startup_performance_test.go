package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/server"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

type startupInventoryNative struct {
	appCanonicalNative
	inventories int
	source      string
}

func (p *startupInventoryNative) HistoryIdentity(ctx context.Context) (platforms.HistoryIdentity, error) {
	if p.source != "" {
		return platforms.HistoryIdentity{Platform: "test", SourceKey: p.source}, nil
	}
	return p.appCanonicalNative.HistoryIdentity(ctx)
}

func (p *startupInventoryNative) Sessions(ctx context.Context, dir string, after int64) ([]db.Session, error) {
	p.inventories++
	return p.appCanonicalNative.Sessions(ctx, dir, after)
}

func TestCompositionSchedulesHistoryWithoutPerformingIt(t *testing.T) {
	core, err := state.Open(statetest.Path(t, server.ModuleSchemas()...), server.ModuleSchemas()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	history, err := statev2.Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = history.Close() })
	native := &startupInventoryNative{}
	registry := platforms.NewRegistry()
	work, err := composeCanonicalSessionCore(t.Context(), core, history, registry, []platforms.Integration{native}, "owner")
	if err != nil || work == nil || native.inventories != 0 {
		t.Fatalf("composition performed historical work: calls=%d scheduled=%v err=%v", native.inventories, work != nil, err)
	}
	if !registry.CanonicalSessionIDs() {
		t.Fatal("cheap composition did not build canonical routing")
	}
	if err := work(t.Context()); err != nil {
		t.Fatal(err)
	}
	compose := func() func(context.Context) error {
		t.Helper()
		work, err := composeCanonicalSessionCore(t.Context(), core, history, platforms.NewRegistry(), []platforms.Integration{native}, "owner")
		if err != nil {
			t.Fatal(err)
		}
		return work
	}
	if compose() != nil {
		t.Fatal("completed cutover scheduled another historical pass")
	}
	native.source = "replacement-source"
	if compose() == nil || native.inventories != 1 {
		t.Fatal("new physical source reused a completed source's cutover receipt")
	}
	native.source = ""
	if err := history.CompletePreparation(t.Context(), "owner", "restored-history-receipt"); err != nil {
		t.Fatal(err)
	}
	if compose() == nil {
		t.Fatal("core receipt bypassed preparation after history replacement")
	}
}

func TestInterruptedHistoryPlanDoesNotRediscoverCanonicalParentsAsNative(t *testing.T) {
	core, err := state.Open(statetest.Path(t, server.ModuleSchemas()...), server.ModuleSchemas()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	history, err := statev2.Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = history.Close() })
	if err := history.RegisterInstance(t.Context(), statev2.Instance{ID: "old", OwnerID: "owner", Platform: "test", SourceKey: "inactive"}); err != nil {
		t.Fatal(err)
	}
	catalogue, err := sessioncatalog.New(core)
	if err != nil {
		t.Fatal(err)
	}
	source := sessioncatalog.Source{OwnerID: "owner", Platform: "test", SourceKey: "inactive"}
	if _, err := catalogue.Reconcile(t.Context(), source, []sessioncatalog.NativeSession{{ID: "parent"}, {ID: "child", ParentID: "parent"}}); err != nil {
		t.Fatal(err)
	}
	parent, err := catalogue.FindBinding(t.Context(), sessioncatalog.Binding{OwnerID: "owner", Platform: "test", SourceKey: "inactive", NativeID: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := catalogue.FindBinding(t.Context(), sessioncatalog.Binding{OwnerID: "owner", Platform: "test", SourceKey: "inactive", NativeID: "child"})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a committed parent batch followed by a crash before the child's
	// batch. The retained child now names a canonical parent, not a native ID.
	for _, row := range []statev2.Session{{Key: statev2.Key{InstanceID: "old", SessionID: parent.ID}}, {Key: statev2.Key{InstanceID: "old", SessionID: "child"}, ParentID: parent.ID}} {
		if _, err := history.Commit(t.Context(), statev2.Batch{Session: row}); err != nil {
			t.Fatal(err)
		}
	}
	if err := history.PlanSessionRekeys(t.Context(), "old", map[string]string{"child": child.ID}); err != nil {
		t.Fatal(err)
	}
	if err := history.SetSessionRekeyInventoryReady(t.Context(), "owner", true); err != nil {
		t.Fatal(err)
	}
	if err := prepareCanonicalSessionCore(t.Context(), core, history, platforms.NewRegistry(), nil, "owner"); err != nil {
		t.Fatal(err)
	}
	rows, err := catalogue.List(t.Context(), "owner", 100, 0)
	if err != nil || len(rows) != 2 {
		t.Fatalf("resume allocated a second parent identity: %+v %v", rows, err)
	}
	page, err := history.Read(t.Context(), statev2.Key{InstanceID: "old", SessionID: child.ID}, 10, 0)
	if err != nil || page.Session.ParentID != parent.ID {
		t.Fatalf("resume lost canonical parent: %+v %v", page, err)
	}
}

func TestCanonicalRestartDoesNotRepeatHistoricalInventory(t *testing.T) {
	core, err := state.Open(statetest.Path(t, server.ModuleSchemas()...), server.ModuleSchemas()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	history, err := statev2.Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = history.Close() })
	native := &startupInventoryNative{}
	for range 3 {
		if err := prepareCanonicalSessionCore(t.Context(), core, history, platforms.NewRegistry(), []platforms.Integration{native}, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	if native.inventories != 1 {
		t.Fatalf("startup repeated historical session inventory: got %d calls, want 1", native.inventories)
	}
}

func TestRestartRestoresLocalReferencesDiscoveredAfterCutover(t *testing.T) {
	core, err := state.Open(statetest.Path(t, server.ModuleSchemas()...), server.ModuleSchemas()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	history, err := statev2.Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = history.Close() })
	if err := core.PinSession(t.Context(), "test", "late-native"); err != nil {
		t.Fatal(err)
	}
	prepare := func() {
		t.Helper()
		if err := prepareCanonicalSessionCore(t.Context(), core, history, platforms.NewRegistry(), []platforms.Integration{&appCanonicalNative{}}, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	prepare()
	catalogue, err := sessioncatalog.New(core)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalogue.Reconcile(t.Context(), sessioncatalog.Source{OwnerID: "owner", Platform: "test", SourceKey: "source"}, []sessioncatalog.NativeSession{{ID: "late-native"}}); err != nil {
		t.Fatal(err)
	}
	row, err := catalogue.FindBinding(t.Context(), sessioncatalog.Binding{OwnerID: "owner", Platform: "test", SourceKey: "source", NativeID: "late-native"})
	if err != nil {
		t.Fatal(err)
	}
	prepare()
	refs, err := core.SessionReferences(t.Context(), "test")
	if err != nil || len(refs) != 1 || refs[0] != row.ID {
		t.Fatalf("completed cutover hid a newly resolvable local reference: %+v %v", refs, err)
	}
}
