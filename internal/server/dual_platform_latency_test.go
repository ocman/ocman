package server

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/claudeprovider"
	"github.com/NoUseFreak/ocman/internal/claudeprovider/claudetest"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	opencodeplatform "github.com/NoUseFreak/ocman/internal/platforms/opencode"
	"github.com/NoUseFreak/ocman/internal/platforms/provider"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

// TestDualPlatformKeepsOpenCodeListing is NFR-1's evidence that enabling
// Claude Code beside OpenCode leaves the OpenCode session list unchanged in
// content and latency. Two servers share one seeded OpenCode database; the
// second also registers the real Claude provider over the fixture
// transcripts. Requests alternate between them so machine load hits both
// samples alike. The bound is loose on purpose: it catches a serialised or
// per-request Claude scan, not scheduler noise.
func TestDualPlatformKeepsOpenCodeListing(t *testing.T) {
	const sessions, rounds = 300, 60
	dir, err := os.MkdirTemp("", "ocl-") // short: unix socket path limit
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	database := seedOpenCodeSessions(t, filepath.Join(dir, "opencode.db"), sessions)

	single := latencyServer(t, dir, "single", database)
	dual := latencyServer(t, dir, "dual", database)
	dual.registry.Register(claudeFixtureAdapter(t, dir))

	path := "/api/sessions?limit=0"
	waitClaude(t, "both Claude fixture sessions listed", func() bool {
		return countPlatform(listVia(t, dual, path), "claudecode") == 2
	})
	if a, b := onlyPlatform(listVia(t, single, path), "opencode"), onlyPlatform(listVia(t, dual, path), "opencode"); len(a) != sessions || !reflect.DeepEqual(a, b) {
		t.Fatalf("OpenCode rows differ with Claude enabled: %d vs %d", len(a), len(b))
	}

	var one, both []time.Duration
	for range rounds {
		one = append(one, timeList(t, single, path))
		both = append(both, timeList(t, dual, path))
	}
	m1, m2 := median(one), median(both)
	t.Logf("GET %s, %d OpenCode sessions, %d rounds: median opencode %v, opencode+claudecode %v", path, sessions, rounds, m1, m2)
	if m2 > 2*m1+25*time.Millisecond {
		t.Fatalf("dual-platform median %v exceeds 2x OpenCode-only %v + 25ms", m2, m1)
	}
}

func seedOpenCodeSessions(t *testing.T, path string, n int) *db.DB {
	t.Helper()
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	stmts := []string{
		`CREATE TABLE session (id TEXT PRIMARY KEY, project_id TEXT NOT NULL DEFAULT '', parent_id TEXT, title TEXT NOT NULL DEFAULT '',
			directory TEXT NOT NULL DEFAULT '', time_created INTEGER NOT NULL DEFAULT 0, time_updated INTEGER NOT NULL DEFAULT 0,
			summary_additions INTEGER, summary_deletions INTEGER, summary_files INTEGER, share_url TEXT)`,
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, time_created INTEGER NOT NULL DEFAULT 0, data TEXT NOT NULL DEFAULT '{}')`,
		`CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT NOT NULL, session_id TEXT NOT NULL, time_created INTEGER NOT NULL DEFAULT 0, data TEXT NOT NULL DEFAULT '{}')`,
	}
	now := time.Now().UnixMilli()
	for i := range n {
		at := now - int64(i)*60_000
		id := fmt.Sprintf("ses_%04d", i)
		stmts = append(stmts,
			fmt.Sprintf(`INSERT INTO session (id, title, directory, time_created, time_updated) VALUES ('%s', 'Session %d', '/work/p%d', %d, %d)`, id, i, i%7, at, at),
			fmt.Sprintf(`INSERT INTO message (id, session_id, time_created, data) VALUES ('msg_%04da', '%s', %d, '{"role":"user","time":{"created":%d}}')`, i, id, at, at),
			fmt.Sprintf(`INSERT INTO message (id, session_id, time_created, data) VALUES ('msg_%04db', '%s', %d, '{"role":"assistant","finish":"stop","modelID":"m","providerID":"p","cost":0.01,"tokens":{"input":10,"output":5},"time":{"created":%d,"completed":%d}}')`, i, id, at+1, at+1, at+2),
		)
	}
	for _, s := range stmts {
		if _, err := raw.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	database, err := db.OpenReadWrite(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func latencyServer(t *testing.T, dir, name string, database *db.DB) *Server {
	t.Helper()
	stateDB, err := state.Open(statetest.At(t, filepath.Join(dir, name+"-state.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stateDB.Close() })
	reg := platforms.NewRegistry()
	reg.Register(opencodeplatform.New(database, stateDB))
	srv := New(database, stateDB, "127.0.0.1:0", reg, nil)
	srv.runtime = fakeRuntime{}
	return srv
}

// claudeFixtureAdapter serves the Claude provider over the committed
// transcript fixtures with the fake CLI, as `-platforms claudecode` does.
func claudeFixtureAdapter(t *testing.T, dir string) *provider.Adapter {
	t.Helper()
	config := filepath.Join(dir, "config")
	copyFixtureTree(t, filepath.Join("..", "claudeprovider", "testdata", "projects"), filepath.Join(config, "projects"))
	sup, err := claudeprovider.NewSupervisor(t.Context(), claudeprovider.Options{
		IndexPath: filepath.Join(dir, "claude-index.db"), ConfigDir: config, CLI: claudetest.Binary(t), Sweep: time.Hour,
		MaxProcs: 4, IdleTimeout: time.Minute,
	}, filepath.Join(dir, "spool"))
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "s")
	go func() { _ = sup.Serve(t.Context(), socket) }()
	var adapter *provider.Adapter
	for deadline := time.Now().Add(5 * time.Second); adapter == nil; time.Sleep(10 * time.Millisecond) {
		if adapter, err = provider.New(t.Context(), socket); err != nil && time.Now().After(deadline) {
			t.Fatal(err)
		}
	}
	t.Cleanup(adapter.Close)
	return adapter
}

func copyFixtureTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.Walk(from, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(to, strings.TrimPrefix(path, from))
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func listVia(t *testing.T, srv *Server, path string) []db.Session {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.handleSessions(rec, httptest.NewRequest(http.MethodGet, path, nil))
	var out []db.Session
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		t.Fatalf("GET %s = %d %s", path, rec.Code, rec.Body)
	}
	return out
}

func timeList(t *testing.T, srv *Server, path string) time.Duration {
	start := time.Now()
	listVia(t, srv, path)
	return time.Since(start)
}

func onlyPlatform(in []db.Session, id string) []db.Session {
	var out []db.Session
	for _, s := range in {
		if s.Platform == id {
			out = append(out, s)
		}
	}
	return out
}

func countPlatform(in []db.Session, id string) int { return len(onlyPlatform(in, id)) }

func median(d []time.Duration) time.Duration {
	s := slices.Clone(d)
	slices.Sort(s)
	return s[len(s)/2]
}
