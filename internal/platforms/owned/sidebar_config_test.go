package owned

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/configfile"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/state"
)

func TestLocalSidebarUsesFileArchivePreference(t *testing.T) {
	fixture, native, id := storedFixture(t)
	live := &sidebarNative{nativePlatform: native, t: t}
	corePath, err := fixture.store.FilePath(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	core, err := state.Open(corePath, sessioncatalog.Schema)
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	if err := core.SetSetting(t.Context(), "session.archive_resurface", "activity"); err != nil {
		t.Fatal(err)
	}
	if err := core.ArchiveSession(t.Context(), "test", id, 25); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := core.EnableFileConfig(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	adapters, err := BuildCore(t.Context(), core, fixture.history, fixture.store, fixture.owner, []platforms.Integration{live})
	if err != nil {
		t.Fatal(err)
	}
	a := adapters[0].(*Adapter)
	worker := &sidebarWorker{adapter: a}
	if err := worker.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = worker.Close(t.Context()) })
	if err := worker.snapshot(t.Context()); err != nil {
		t.Fatal(err)
	}
	check := func(wantArchived bool) {
		t.Helper()
		source, err := a.SidebarSource(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, grouped := range []bool{false, true} {
			rows, err := a.SidebarSessions(t.Context(), platforms.SidebarQuery{})
			if grouped {
				rows, err = a.SidebarSessionsFor(t.Context(), []platforms.SidebarSource{source}, platforms.SidebarQuery{})
			}
			if err != nil || len(rows) != 1 || rows[0].Archived != wantArchived {
				t.Fatalf("local sidebar used stale DB policy, grouped=%t: %+v, %v", grouped, rows, err)
			}
		}
	}
	if err := core.SetSetting(t.Context(), "session.archive_resurface", "halt"); err != nil {
		t.Fatal(err)
	}
	check(true)
	if err := core.SetSetting(t.Context(), "session.archive_resurface", "activity"); err != nil {
		t.Fatal(err)
	}
	check(false)
	if err := (&configfile.File{Path: path}).Update(func(doc *configfile.Document) error {
		doc.Delete("settings", "session.archive_resurface")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	check(true)
	var backup string
	if err := core.SQL().QueryRowContext(t.Context(), `SELECT value FROM setting WHERE key='session.archive_resurface'`).Scan(&backup); err != nil || backup != "activity" {
		t.Fatal("sidebar fix modified the retained DB backup")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SidebarSessions(t.Context(), platforms.SidebarQuery{}); err == nil {
		t.Fatal("missing configuration fell back to the stale sidebar policy")
	}
}
