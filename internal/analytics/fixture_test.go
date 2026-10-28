package analytics

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/ocman/ocman/internal/conversationingest"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/module/moduletest"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/statev2"
)

// fixture is a module over a fresh OpenCode database (raw is its writable
// handle, for seeding) and a fresh state database.
type fixture struct {
	*Module
	raw         *sql.DB
	legacy      *db.DB
	mux         *http.ServeMux
	t           *testing.T
	seedHistory *HistoryAnalytics
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Close() })
	if _, err := raw.Exec(`
		CREATE TABLE session (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL DEFAULT '',
			parent_id TEXT,
			title TEXT NOT NULL DEFAULT '',
			directory TEXT NOT NULL DEFAULT '',
			time_created INTEGER NOT NULL DEFAULT 0,
			time_updated INTEGER NOT NULL DEFAULT 0,
			summary_additions INTEGER,
			summary_deletions INTEGER,
			summary_files INTEGER,
			share_url TEXT
		);
		CREATE TABLE message (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			time_created INTEGER NOT NULL DEFAULT 0,
			data TEXT NOT NULL DEFAULT '{}'
		);
		CREATE TABLE part (
			id TEXT PRIMARY KEY,
			message_id TEXT NOT NULL,
			session_id TEXT NOT NULL,
			time_created INTEGER NOT NULL DEFAULT 0,
			data TEXT NOT NULL DEFAULT '{}'
		);`); err != nil {
		t.Fatal(err)
	}
	database, err := db.OpenReadWrite(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	store, err := state.Open(statetest.At(t, filepath.Join(dir, "state.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	history, err := statev2.Open(filepath.Join(dir, "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = history.Close() })
	if err := history.RegisterInstance(t.Context(), statev2.Instance{ID: "fixture", OwnerID: "owner", Platform: "test", SourceKey: "fixture"}); err != nil {
		t.Fatal(err)
	}
	h := &HistoryAnalytics{Store: history, Instances: func() ([]string, bool) { return []string{"fixture"}, true }}
	f := &fixture{Module: newTestModule(Deps{Store: store, HistoryAnalytics: func() *HistoryAnalytics { return h }}), raw: raw, legacy: database, t: t, seedHistory: h}
	f.mux = routes(f.Module)
	return f
}

func newTestModule(deps Deps) *Module { return NewModule(deps) }

func routes(m *Module) *http.ServeMux { return moduletest.Mount(m) }

func routesWith(m *Module, g module.Guards) *http.ServeMux {
	r := moduletest.Router{Mux: http.NewServeMux(), Auth: g.Auth, Localhost: g.Localhost}
	m.Routes(r)
	return r.Mux
}

// serve sends one request through the module's routes.
func (f *fixture) serve(method, target, body string) *httptest.ResponseRecorder {
	if f.legacy != nil && f.deps.HistoryAnalytics != nil && f.deps.HistoryAnalytics() == f.seedHistory {
		f.importFixture()
	}
	return serve(f.mux, method, target, body)
}

// Seed old-style test rows through ingestion, then exercise production routes.
// Tests that install a different history store manage its contents themselves.
func (f *fixture) importFixture() {
	f.t.Helper()
	entries, err := f.legacy.HistoryInventory(f.t.Context())
	if err != nil {
		f.t.Fatal(err)
	}
	for _, entry := range entries {
		snapshot, err := f.legacy.HistorySnapshot(f.t.Context(), entry.ID)
		if err != nil {
			f.t.Fatal(err)
		}
		key := statev2.Key{InstanceID: "fixture", SessionID: entry.ID}
		batch, _, err := conversationingest.Normalize(key, snapshot)
		if err != nil {
			f.t.Fatal(err)
		}
		if old, err := f.seedHistory.Store.Metadata(f.t.Context(), key); err == nil {
			batch.ExpectedRevision = old.Session.Revision
		}
		if _, err := f.seedHistory.Store.Commit(f.t.Context(), batch); err != nil {
			f.t.Fatal(err)
		}
	}
}

func serve(mux *http.ServeMux, method, target, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, target, strings.NewReader(body)))
	return rec
}
