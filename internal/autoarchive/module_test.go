package autoarchive

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/module/moduletest"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
)

// archivePlatform answers inactive-session reads through inactive.
type archivePlatform struct {
	platforms.Platform
	id       string
	down     bool
	inactive func(ctx context.Context) ([]db.SessionArchiveCandidate, error)
}

func (p *archivePlatform) ID() platforms.ID               { return platforms.ID(p.id) }
func (p *archivePlatform) Available(context.Context) bool { return !p.down }
func (p *archivePlatform) SessionsInactiveBefore(ctx context.Context, _ int64) ([]db.SessionArchiveCandidate, error) {
	return p.inactive(ctx)
}

// projectHost lists projects and records opencode stops.
type projectHost struct {
	hostsvc.Host
	projects []db.ProjectStats
	err      error
	stopped  []string
}

func (h *projectHost) Projects(context.Context) ([]db.ProjectStats, error) { return h.projects, h.err }
func (h *projectHost) StopProjectOpencode(_ context.Context, req hostsvc.EnsureProjectOpencodeRequest) error {
	h.stopped = append(h.stopped, req.ProjectDir)
	return errors.New("no tmux session") // best effort: archiving proceeds
}

func newModule(store *state.DB, host *projectHost, ps ...platforms.Platform) *Module {
	return NewModule(Deps{
		Store:         store,
		Platforms:     func() []platforms.Platform { return ps },
		Local:         func() hostsvc.Host { return host },
		StoredHistory: func() bool { return true },
	})
}

func daysAgo(n int) int64 { return time.Now().Add(-time.Duration(n) * 24 * time.Hour).UnixMilli() }

func call(m *Module, method, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	moduletest.Mount(m).ServeHTTP(rec, httptest.NewRequest(method, "/api/settings/auto-archive", strings.NewReader(body)))
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) Settings {
	t.Helper()
	var s Settings
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatalf("decoding %q: %v", rec.Body.String(), err)
	}
	return s
}

func TestSettingsRoute(t *testing.T) {
	m := newModule(statetest.Open(t), nil)
	if rec := call(m, http.MethodGet, ""); rec.Code != http.StatusOK || decode(t, rec) != (Settings{Enabled: true, TTLDays: 7}) {
		t.Fatalf("GET default = %d %s", rec.Code, rec.Body)
	}
	if rec := call(m, http.MethodPost, `{"enabled":false,"ttlDays":30}`); rec.Code != http.StatusOK || decode(t, rec) != (Settings{TTLDays: 30}) {
		t.Fatalf("POST = %d %s", rec.Code, rec.Body)
	}
	if rec := call(m, http.MethodGet, ""); decode(t, rec) != (Settings{TTLDays: 30}) {
		t.Fatalf("GET saved = %s", rec.Body)
	}
	for _, tc := range []struct {
		method, body string
		want         int
	}{
		{http.MethodPost, `{"enabled":true,"ttlDays":0}`, http.StatusBadRequest},
		{http.MethodPost, `{"enabled":true,"ttlDays":3651}`, http.StatusBadRequest},
		{http.MethodPost, `{bad`, http.StatusBadRequest},
		{http.MethodDelete, "", http.StatusMethodNotAllowed},
	} {
		if rec := call(m, tc.method, tc.body); rec.Code != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.body, rec.Code, tc.want)
		}
	}
	if rec := call(newModule(nil, nil), http.MethodGet, ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET without store = %d", rec.Code)
	}
}

func TestSettingsRejectStoredGarbage(t *testing.T) {
	store := statetest.Open(t)
	m := newModule(store, nil)
	for _, value := range []string{`{`, `{"enabled":true,"ttlDays":0}`} {
		if err := store.SetSetting(t.Context(), settingKey, value); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Settings(t.Context()); err == nil {
			t.Errorf("stored %s read without error", value)
		}
	}
	if err := m.SetSettings(t.Context(), Settings{TTLDays: 0}); err == nil {
		t.Fatal("SetSettings accepted ttlDays 0")
	}
}

func TestArchiveSessionsHonoursTTLAndUnarchive(t *testing.T) {
	store := statetest.Open(t)
	ctx := t.Context()
	p := &archivePlatform{id: "fake", inactive: func(context.Context) ([]db.SessionArchiveCandidate, error) {
		return []db.SessionArchiveCandidate{{ID: "old", TimeUpdated: daysAgo(8)}, {ID: "kept", TimeUpdated: daysAgo(8)}}, nil
	}}
	failing := &archivePlatform{id: "broken", inactive: func(context.Context) ([]db.SessionArchiveCandidate, error) {
		return nil, errors.New("unreachable")
	}}
	offline := &archivePlatform{id: "offline", down: true}
	if err := store.UnarchiveSession(ctx, "fake", "kept"); err != nil {
		t.Fatal(err)
	}
	newModule(store, nil, failing, offline, p).ArchiveSessions(ctx)
	archived, err := store.ArchivedSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := archived[state.Key{Platform: "fake", SessionID: "old"}]; !ok {
		t.Errorf("inactive session not archived: %+v", archived)
	}
	if _, ok := archived[state.Key{Platform: "fake", SessionID: "kept"}]; ok {
		t.Error("recently unarchived session was re-archived")
	}
}

func TestArchiveDisabledDoesNothing(t *testing.T) {
	store := statetest.Open(t)
	host := &projectHost{projects: []db.ProjectStats{{Directory: "/tmp/stale", LastUsed: daysAgo(30)}}}
	scanned := false
	p := &archivePlatform{id: "fake", inactive: func(context.Context) ([]db.SessionArchiveCandidate, error) {
		scanned = true
		return nil, nil
	}}
	m := newModule(store, host, p)
	if err := m.SetSettings(t.Context(), Settings{Enabled: false, TTLDays: 7}); err != nil {
		t.Fatal(err)
	}
	m.ArchiveSessions(t.Context())
	m.ArchiveProjects(t.Context())
	if scanned || len(host.stopped) != 0 {
		t.Fatalf("disabled auto-archive scanned=%v stopped=%v", scanned, host.stopped)
	}
}

func TestArchiveProjects(t *testing.T) {
	store := statetest.Open(t)
	ctx := t.Context()
	host := &projectHost{projects: []db.ProjectStats{
		{Directory: "/tmp/stale", LastUsed: daysAgo(4)},
		{Directory: "/tmp/active", LastUsed: daysAgo(2)},
		{Directory: "/tmp/kept", LastUsed: daysAgo(30)},
		{Directory: "/tmp/done", LastUsed: daysAgo(30)},
	}}
	m := newModule(store, host)
	if err := m.SetSettings(ctx, Settings{Enabled: true, TTLDays: 3}); err != nil {
		t.Fatal(err)
	}
	if err := store.UnarchiveProject(ctx, state.LocalRemoteID, "/tmp/kept"); err != nil {
		t.Fatal(err)
	}
	if err := store.ArchiveProject(ctx, state.LocalRemoteID, "/tmp/done"); err != nil {
		t.Fatal(err)
	}
	m.ArchiveProjects(ctx)
	archived, err := store.ArchivedProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	key := func(root string) state.ProjectKey { return state.ProjectKey{RemoteID: state.LocalRemoteID, Root: root} }
	if _, ok := archived[key("/tmp/stale")]; !ok {
		t.Errorf("stale project not archived: %+v", archived)
	}
	for _, root := range []string{"/tmp/active", "/tmp/kept"} {
		if _, ok := archived[key(root)]; ok {
			t.Errorf("%s archived", root)
		}
	}
	if len(host.stopped) != 1 || host.stopped[0] != "/tmp/stale" {
		t.Fatalf("stopped = %v, want only /tmp/stale", host.stopped)
	}
}

func TestArchiveProjectsSkipsWithoutHistoryOrProjects(t *testing.T) {
	store := statetest.Open(t)
	host := &projectHost{err: errors.New("no projects")}
	m := newModule(store, host)
	m.ArchiveProjects(t.Context()) // listing failure is logged
	m.d.StoredHistory = func() bool { return false }
	host.err, host.projects = nil, []db.ProjectStats{{Directory: "/tmp/stale", LastUsed: 1}}
	m.ArchiveProjects(t.Context())
	if len(host.stopped) != 0 {
		t.Fatalf("archived without stored history: %v", host.stopped)
	}
	newModule(nil, host).ArchiveProjects(t.Context())
	newModule(nil, host).ArchiveSessions(t.Context()) // logged, not a panic
}

// A remote that never answers is cut off at the platform budget, so the
// project pass still runs within the same tick.
func TestRemoteTimeoutDoesNotStarveLocalMaintenance(t *testing.T) {
	store := statetest.Open(t)
	var budget time.Duration
	remote := &archivePlatform{id: "remote", inactive: func(ctx context.Context) ([]db.SessionArchiveCandidate, error) {
		deadline, _ := ctx.Deadline()
		budget = time.Until(deadline)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	host := &projectHost{projects: []db.ProjectStats{{Directory: "/tmp/stale", LastUsed: daysAgo(14)}}}
	m := newModule(store, host, remote)
	if m.platformTimeout != 10*time.Second {
		t.Fatalf("platformTimeout = %v, want 10s", m.platformTimeout)
	}
	m.platformTimeout = 100 * time.Millisecond
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		archived, err := store.ArchivedProjects(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := archived[state.ProjectKey{RemoteID: state.LocalRemoteID, Root: "/tmp/stale"}]; ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("remote timeout prevented local project archiving")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if budget <= 0 || budget > m.platformTimeout {
		t.Errorf("remote RPC budget = %v, want <= %v", budget, m.platformTimeout)
	}
	newModule(nil, host).Run(ctx) // returns at once without a store
}

// snoozeDuringScan snoozes (and optionally wakes) s1 after the archive
// snapshot has been read, as a concurrent user request would.
func snoozeDuringScan(store *state.DB, until int64, wake bool) *archivePlatform {
	return &archivePlatform{id: "fake", inactive: func(ctx context.Context) ([]db.SessionArchiveCandidate, error) {
		if err := store.SnoozeSession(ctx, "fake", "s1", until); err != nil {
			return nil, err
		}
		if wake {
			if _, err := store.WakeSnoozedSessions(ctx, until); err != nil {
				return nil, err
			}
		}
		return []db.SessionArchiveCandidate{{ID: "s1", TimeUpdated: 1}}, nil
	}}
}

func TestArchivePreservesConcurrentSnooze(t *testing.T) {
	store := statetest.Open(t)
	until := time.Now().Add(time.Hour).UnixMilli()
	newModule(store, nil, snoozeDuringScan(store, until, false)).ArchiveSessions(t.Context())
	keys, err := store.WakeSnoozedSessions(t.Context(), until)
	if err != nil || len(keys) != 1 || keys[0] != (state.Key{Platform: "fake", SessionID: "s1"}) {
		t.Fatalf("auto-archive erased accepted snooze: %v %v", keys, err)
	}
}

func TestArchivePreservesSnoozeThatWakesDuringScan(t *testing.T) {
	store := statetest.Open(t)
	until := time.Now().Add(time.Hour).UnixMilli()
	newModule(store, nil, snoozeDuringScan(store, until, true)).ArchiveSessions(t.Context())
	archived, err := store.ArchivedSessions(t.Context())
	_, ok := archived[state.Key{Platform: "fake", SessionID: "s1"}]
	if err != nil || ok {
		t.Fatalf("auto-archive re-hid awake session: %v %v", ok, err)
	}
}

func TestArchiveScansWhenUnarchiveLookupFails(t *testing.T) {
	store := statetest.Open(t)
	scanned := false
	p := &archivePlatform{id: "fake", inactive: func(context.Context) ([]db.SessionArchiveCandidate, error) {
		scanned = true
		return nil, nil
	}}
	if _, err := store.SQL().Exec(`DROP TABLE unarchived_entity`); err != nil {
		t.Fatal(err)
	}
	newModule(store, nil, p).ArchiveSessions(t.Context())
	if !scanned {
		t.Fatal("unarchive lookup failure aborted the inactivity scan")
	}
}
