package analytics

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

// fixture is a module over a fresh OpenCode database (raw is its writable
// handle, for seeding) and a fresh state database.
type fixture struct {
	*Module
	raw *sql.DB
	mux *http.ServeMux
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
	f := &fixture{Module: newTestModule(Deps{DB: database, Store: store}), raw: raw}
	f.mux = routes(f.Module)
	return f
}

func passthrough(h http.HandlerFunc) http.HandlerFunc { return h }

// newTestModule fills in passthrough guards.
func newTestModule(deps Deps) *Module {
	if deps.Guards.Auth == nil {
		deps.Guards.Auth = passthrough
	}
	if deps.Guards.Localhost == nil {
		deps.Guards.Localhost = passthrough
	}
	return NewModule(deps)
}

type muxRouter struct{ mux *http.ServeMux }

func (r muxRouter) Handle(p string, h http.HandlerFunc)       { r.mux.HandleFunc(p, h) }
func (r muxRouter) Local(p string, h http.HandlerFunc)        { r.mux.HandleFunc(p, h) }
func (r muxRouter) Setting(p string, h http.HandlerFunc)      { r.mux.HandleFunc(p, h) }
func (r muxRouter) LocalSetting(p string, h http.HandlerFunc) { r.mux.HandleFunc(p, h) }

var _ module.Router = muxRouter{}

func routes(m *Module) *http.ServeMux {
	mux := http.NewServeMux()
	m.Routes(muxRouter{mux})
	return mux
}

// serve sends one request through the module's routes.
func (f *fixture) serve(method, target, body string) *httptest.ResponseRecorder {
	return serve(f.mux, method, target, body)
}

func serve(mux *http.ServeMux, method, target, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, target, strings.NewReader(body)))
	return rec
}
