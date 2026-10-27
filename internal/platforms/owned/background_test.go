package owned

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/state"
)

func TestBackgroundLifecycleControlsArchiveWithoutChangingForeground(t *testing.T) {
	a, p, id := storedFixture(t)
	a.native = &sidebarNative{nativePlatform: p, t: t, observations: map[string]platforms.LiveState{"native": {Status: "idle"}}}
	worker := &sidebarWorker{adapter: a}
	if err := worker.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = worker.Close(t.Context()) })
	path, _ := a.store.FilePath(t.Context())
	core, err := state.Open(path, sessioncatalog.Schema)
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	read := func() bool {
		t.Helper()
		if err := worker.snapshot(t.Context()); err != nil {
			t.Fatal(err)
		}
		rows, err := a.SidebarSessions(t.Context(), platforms.SidebarQuery{})
		if err != nil || len(rows) != 1 {
			t.Fatalf("rows=%+v err=%v", rows, err)
		}
		if rows[0].Status == "busy" {
			t.Fatal("background work changed foreground status")
		}
		return rows[0].Archived
	}
	rows, err := a.SidebarSessions(t.Context(), platforms.SidebarQuery{})
	if err != nil || len(rows) != 1 {
		t.Fatalf("%+v %v", rows, err)
	}
	if err := core.ArchiveSession(t.Context(), string(a.ID()), id, rows[0].TimeUpdated-1); err != nil {
		t.Fatal(err)
	}
	changes := 0
	a.SetSessionEventSink(platforms.SessionEventSink{OnSessionBackgroundChanged: func(platform, session string) {
		if session != id || platform != string(a.ID()) {
			t.Fatalf("wrong notification %s/%s", platform, session)
		}
		changes++
	}})
	var out bytes.Buffer
	writer := a.SessionEventWriter(t.Context(), &out)
	if _, err := fmt.Fprint(writer, "data: {\"type\":\"ocman.session.background\",\"properties\":{\"sessionID\":\"native\",\"count\":1,\"tasks\":[{\"id\":\"shell\",\"description\":\"pnpm dev\"}]}}\n\n"); err != nil {
		t.Fatal(err)
	}
	if !read() {
		t.Fatal("unfinished background task resurfaced")
	}
	if changes != 1 {
		t.Fatalf("notifications=%d", changes)
	}
	for _, lifecycle := range []string{"service", "task"} {
		if err := a.SetBackgroundLifecycle(t.Context(), platforms.SetBackgroundLifecycleRequest{SessionID: id, TaskID: "shell", Lifecycle: lifecycle}); err != nil {
			t.Fatal(err)
		}
		if archived := read(); archived != (lifecycle == "task") {
			t.Fatalf("%s archived=%v", lifecycle, archived)
		}
	}
	if _, err := fmt.Fprint(writer, "data: {\"type\":\"ocman.session.background\",\"properties\":{\"sessionID\":\"native\",\"count\":0}}\n\n"); err != nil {
		t.Fatal(err)
	}
	if read() {
		t.Fatal("final task completion did not resurface session")
	}
	if changes != 4 {
		t.Fatalf("completion notification missing: %d", changes)
	}
	// A browser replay is not authoritative and must not resurrect the task.
	p.events = "data: {\"type\":\"ocman.session.background\",\"properties\":{\"sessionID\":\"native\",\"count\":1}}\n\n"
	if err := a.ProxyEvents(t.Context(), id, &out, nil); err != nil {
		t.Fatal(err)
	}
	if read() {
		t.Fatal("browser replay resurrected finished work")
	}
	a.observeBackground([]byte(`{"type":"ocman.session.background","properties":{"sessionID":"native","count":1}}`), "")
	if !read() {
		t.Fatal("new work did not delay resurfacing")
	}
	a.native.(*sidebarNative).observations = map[string]platforms.LiveState{}
	a.observeBackground([]byte(`{"backgroundTasks":{}}`), "server.connected")
	if read() || changes != 6 {
		t.Fatalf("reconnect omission did not notify completion: changes=%d", changes)
	}
}

func TestBackgroundSnapshotsAndOverrides(t *testing.T) {
	a, _, _ := fixture(t)
	row, err := a.canonical(t.Context(), "native")
	if err != nil {
		t.Fatal(err)
	}
	a.observeBackground([]byte(`{"payload":{"type":"server.connected","properties":{"backgroundTasks":{"native":3},"backgroundTaskDetails":{"native":[{"id":"server","description":"watch","lifecycle":"service"},{"id":"server","lifecycle":"service"},{"id":"tests","description":"tests","lifecycle":"unknown"}]}}}}`), "")
	get := func() *platforms.BackgroundSnapshot {
		t.Helper()
		s, err := a.BackgroundTasks(t.Context(), row.ID)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	s := get()
	if s.Count != 3 || len(s.Tasks) != 2 || serviceCount(*s) != 1 || s.Tasks[1].Lifecycle != "task" {
		t.Fatalf("snapshot=%+v", s)
	}
	if err := a.SetBackgroundLifecycle(t.Context(), platforms.SetBackgroundLifecycleRequest{SessionID: row.ID, TaskID: "tests", Lifecycle: "service"}); err != nil {
		t.Fatal(err)
	}
	if serviceCount(*get()) != 2 {
		t.Fatal("override missing")
	}
	for _, scope := range [][3]string{{"another-owner", a.source.Platform, a.source.SourceKey}, {a.owner, "another-platform", a.source.SourceKey}, {a.owner, a.source.Platform, "another-source"}} {
		overrides, err := a.store.BackgroundLifecycles(t.Context(), scope[0], scope[1], scope[2])
		if err != nil || len(overrides) != 0 {
			t.Fatalf("override escaped its owner/source: %+v %v", overrides, err)
		}
	}
	if err := a.SetBackgroundLifecycle(t.Context(), platforms.SetBackgroundLifecycleRequest{SessionID: row.ID, TaskID: "missing", Lifecycle: "task"}); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatal(err)
	}
	if err := a.SetBackgroundLifecycle(t.Context(), platforms.SetBackgroundLifecycleRequest{SessionID: row.ID, TaskID: "tests", Lifecycle: "guess"}); !errors.Is(err, sessioncatalog.ErrInvalid) {
		t.Fatal(err)
	}
	for _, raw := range []string{`{`, `{"type":"ocman.session.background","properties":{"sessionID":"native","count":-1}}`, `{"type":"ocman.session.background","properties":{"sessionID":"native","count":1.5}}`, `{"type":"ocman.session.background","properties":{"sessionID":"native"}}`} {
		a.observeBackground([]byte(raw), "")
		if get().Count != 3 {
			t.Fatal("invalid update replaced current snapshot")
		}
	}
	// Explicit snapshot reconciliation clears tasks absent after a reconnect.
	a.observeBackground([]byte(`{"backgroundTasks":{},"backgroundTaskDetails":{}}`), "server.connected")
	if get().Count != 0 {
		t.Fatal("reconnect retained completed work")
	}
	a.observeBackground([]byte(`{"type":"ocman.session.background","properties":{"sessionID":"native","count":2,"tasks":[{"id":5},{"id":"ok","description":"test"}]}}`), "")
	if snapshot := get(); snapshot.Count != 2 || len(snapshot.Tasks) != 1 {
		t.Fatalf("bad details erased valid count: %+v", snapshot)
	}
	a.observeBackground([]byte(`{"type":"ocman.session.background","properties":{"sessionID":"native","count":1,"tasks":"unavailable"}}`), "")
	if snapshot := get(); snapshot.Count != 1 || len(snapshot.Tasks) != 0 {
		t.Fatalf("unavailable details erased unfinished work: %+v", snapshot)
	}
}
