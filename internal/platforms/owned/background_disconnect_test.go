package owned

import (
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/state"
)

type backgroundDisconnectedNative struct{ *sidebarNative }

func (*backgroundDisconnectedNative) CachedSessionAvailable() bool { return false }

func TestBackgroundDisconnectDoesNotMeanTaskCompletion(t *testing.T) {
	a, p, id := storedFixture(t)
	a.native = &backgroundDisconnectedNative{&sidebarNative{nativePlatform: p, t: t, observations: map[string]platforms.LiveState{}}}
	w := &sidebarWorker{adapter: a}
	if err := w.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close(t.Context()) })
	path, _ := a.store.FilePath(t.Context())
	core, err := state.Open(path, sessioncatalog.Schema)
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	rows, err := a.SidebarSessions(t.Context(), platforms.SidebarQuery{})
	if err != nil || len(rows) != 1 {
		t.Fatalf("%+v %v", rows, err)
	}
	if err := core.ArchiveSession(t.Context(), string(a.ID()), id, rows[0].TimeUpdated-1); err != nil {
		t.Fatal(err)
	}
	a.observeBackground([]byte(`{"type":"ocman.session.background","properties":{"sessionID":"native","count":1}}`), "")
	if err := w.snapshot(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows, err = a.SidebarSessions(t.Context(), platforms.SidebarQuery{})
	if err != nil || len(rows) != 1 || !rows[0].Archived || rows[0].BackgroundTaskCount != 1 || rows[0].LiveConnection {
		t.Fatalf("disconnect lost unfinished work: %+v %v", rows, err)
	}
}

func TestBackgroundSourceProbeFailureDoesNotMeanTaskCompletion(t *testing.T) {
	a, p, id := storedFixture(t)
	a.observeBackground([]byte(`{"type":"ocman.session.background","properties":{"sessionID":"native","count":1}}`), "")
	p.source = "replacement-source"
	row, err := a.SessionSummary(t.Context(), id)
	if err != nil || row.BackgroundTaskCount != 1 || row.LiveConnection {
		t.Fatalf("source probe lost unfinished background work: %+v %v", row, err)
	}
}
