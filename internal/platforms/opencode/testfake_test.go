package opencode

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"database/sql"

	_ "modernc.org/sqlite"

	"github.com/NoUseFreak/ocman/internal/db"
)

// opencodeFake is a httptest.Server-backed stand-in for a running
// OpenCode instance. Tests configure responses via the public maps.
//
// AD-4: lives in *_test.go so the production binary can never depend
// on it.
type opencodeFake struct {
	server *httptest.Server

	mu       sync.Mutex
	sessions map[string]json.RawMessage   // sessionID -> /session/{id} body
	messages map[string][]json.RawMessage // sessionID -> /session/{id}/message body items
	// sessionStatus / messagesStatus override the response status for
	// the matching path. Zero = 200.
	sessionStatus  int
	messagesStatus int
	// failJSON, when set, causes the next response to be malformed.
	failJSON bool
	// hits records every path the fake observed, in order. Tests use
	// it to assert the function under test issued the expected
	// requests.
	hits []string
}

// newOpencodeFake returns a fake bound to a httptest server. The
// server is closed automatically when the test ends.
func newOpencodeFake(t *testing.T) *opencodeFake {
	t.Helper()
	f := &opencodeFake{
		sessions: map[string]json.RawMessage{},
		messages: map[string][]json.RawMessage{},
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	t.Cleanup(f.server.Close)
	return f
}

// Port returns the listening port — the production code expects a port
// string (passed to fmt.Sprintf("http://127.0.0.1:%s%s")), so we yield
// it pre-trimmed.
func (f *opencodeFake) Port() string {
	return strings.TrimPrefix(f.server.URL, "http://127.0.0.1:")
}

func (f *opencodeFake) serveHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.hits = append(f.hits, r.URL.Path)
	failJSON := f.failJSON
	f.mu.Unlock()

	// /session/{id}/message  → message list
	if strings.HasPrefix(r.URL.Path, "/session/") && strings.HasSuffix(r.URL.Path, "/message") {
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/session/"), "/message")
		f.mu.Lock()
		status := f.messagesStatus
		msgs := f.messages[id]
		f.mu.Unlock()
		if status != 0 {
			http.Error(w, fmt.Sprintf("upstream %d", status), status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if failJSON {
			_, _ = w.Write([]byte("not json"))
			return
		}
		// Encode the list — wrap json.RawMessage entries in a JSON array.
		buf := []byte("[")
		for i, m := range msgs {
			if i > 0 {
				buf = append(buf, ',')
			}
			buf = append(buf, []byte(m)...)
		}
		buf = append(buf, ']')
		_, _ = w.Write(buf)
		return
	}
	// /session/{id} → single session
	if strings.HasPrefix(r.URL.Path, "/session/") {
		id := strings.TrimPrefix(r.URL.Path, "/session/")
		f.mu.Lock()
		status := f.sessionStatus
		raw, ok := f.sessions[id]
		f.mu.Unlock()
		if status != 0 {
			http.Error(w, fmt.Sprintf("upstream %d", status), status)
			return
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if failJSON {
			_, _ = w.Write([]byte("not json"))
			return
		}
		_, _ = w.Write(raw)
		return
	}
	// /config — empty config so getSessionDefaultsCached doesn't blow up.
	if r.URL.Path == "/config" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
		return
	}
	http.NotFound(w, r)
}

// SetSession registers a /session/{id} response.
func (f *opencodeFake) SetSession(id string, raw json.RawMessage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[id] = raw
}

// AddMessage adds one entry to the /session/{id}/message response.
func (f *opencodeFake) AddMessage(id string, raw json.RawMessage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages[id] = append(f.messages[id], raw)
}

// withTestPort installs a discoverPortsImpl override that maps
// directory → fake.Port. The swap goes through
// setDiscoverPortsImplForTests so it is safe under -race even when
// other tests in the same package read the seam concurrently. The
// previous value and the port cache are restored in t.Cleanup.
func withTestPort(t *testing.T, dir, port string) {
	t.Helper()
	restore := setDiscoverPortsImplForTests(func() map[string]string {
		return map[string]string{dir: port}
	})
	resetPortCacheForTests()
	resetSessionPortAffinityForTests()
	t.Cleanup(func() {
		restore()
		resetPortCacheForTests()
		resetSessionPortAffinityForTests()
	})
}

// newTestDBWithSession builds a writable in-process SQLite DB with the
// minimal OpenCode schema and a single session row. Returned *db.DB is
// closed in t.Cleanup.
func newTestDBWithSession(t *testing.T, sessionID, directory string) *db.DB {
	t.Helper()
	tmp := t.TempDir() + "/opencode.db"
	setup, err := sql.Open("sqlite", tmp)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	_, err = setup.Exec(`
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
		);
	`)
	if err != nil {
		setup.Close()
		t.Fatalf("creating schema: %v", err)
	}
	if _, err := setup.Exec(
		`INSERT INTO session (id, title, directory, time_created, time_updated)
		 VALUES (?, ?, ?, ?, ?)`,
		sessionID, "test", directory, 1000, 1000,
	); err != nil {
		setup.Close()
		t.Fatalf("seed session: %v", err)
	}
	setup.Close()

	database, err := db.OpenReadWrite(tmp)
	if err != nil {
		t.Fatalf("db.OpenReadWrite: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}
