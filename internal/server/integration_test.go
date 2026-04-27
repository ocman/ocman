package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	opencodeplatform "github.com/NoUseFreak/ocman/internal/platforms/opencode"
	"github.com/NoUseFreak/ocman/internal/state"
)

// Ensure the sql import is used (needed for the sqlite3 driver side-effect
// to work alongside the explicit mattn import).
var _ = sql.ErrNoRows
var _ = json.Unmarshal

// testServer creates a Server backed by file-backed SQLite databases.
// The OpenCode DB is created with schema and then opened read-only via db.Open.
func testServer(t *testing.T) *Server {
	t.Helper()
	tmpDir := t.TempDir()

	// Create and seed the OpenCode database using a writable connection first.
	tmpOC := tmpDir + "/opencode.db"
	setupDB, err := sql.Open("sqlite3", tmpOC)
	if err != nil {
		t.Fatalf("opening temp oc file: %v", err)
	}
	_, err = setupDB.Exec(`
		CREATE TABLE session (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL DEFAULT '',
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
		setupDB.Close()
		t.Fatalf("creating oc schema: %v", err)
	}
	setupDB.Close()

	// Open in read-write mode for tests (the production code uses read-only,
	// but tests need to work with a freshly created empty database).
	database, err := db.OpenReadWrite(tmpOC)
	if err != nil {
		t.Fatalf("opening db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	// State database
	stateDB, err := state.Open(tmpDir + "/state.db")
	if err != nil {
		t.Fatalf("opening state db: %v", err)
	}
	t.Cleanup(func() { stateDB.Close() })

	reg := platforms.NewRegistry()
	reg.Register(opencodeplatform.New(database, stateDB))
	return New(database, stateDB, "127.0.0.1:0", reg, nil)
}

// --- Handler integration tests ---

func TestHandleStats_Empty(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest("GET", "/api/stats", nil)
	rr := httptest.NewRecorder()
	srv.handleStats(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var stats map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &stats); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}
	if stats["totalSessions"].(float64) != 0 {
		t.Errorf("expected 0 sessions, got %v", stats["totalSessions"])
	}
}

func TestHandleMetrics_Empty(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest("GET", "/api/metrics?days=30", nil)
	rr := httptest.NewRecorder()
	srv.handleMetrics(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}
	if _, ok := payload["summary"]; !ok {
		t.Fatalf("expected summary in metrics payload")
	}
}

func TestHandleProjects_Empty(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest("GET", "/api/projects", nil)
	rr := httptest.NewRecorder()
	srv.handleProjects(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestHandleProjects_UsesInMemoryIndexUntilRefresh(t *testing.T) {
	srv, rawDB := testServerWithRawDB(t)
	defer rawDB.Close()

	_, err := rawDB.Exec(
		`INSERT INTO session (id, title, directory, time_created, time_updated)
		 VALUES ('s-1', 'one', '/repo/one', 1000, 1000)`,
	)
	if err != nil {
		t.Fatalf("seeding first project session: %v", err)
	}

	if err := srv.refreshProjectsIndex(); err != nil {
		t.Fatalf("initial projects refresh: %v", err)
	}

	_, err = rawDB.Exec(
		`INSERT INTO session (id, title, directory, time_created, time_updated)
		 VALUES ('s-2', 'two', '/repo/two', 2000, 2000)`,
	)
	if err != nil {
		t.Fatalf("seeding second project session: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/projects", nil)
	rr := httptest.NewRecorder()
	srv.handleProjects(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var projects []db.ProjectStats
	if err := json.Unmarshal(rr.Body.Bytes(), &projects); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}
	if len(projects) != 1 || projects[0].Directory != "/repo/one" {
		t.Fatalf("expected stale in-memory index with one project, got %+v", projects)
	}

	if err := srv.refreshProjectsIndex(); err != nil {
		t.Fatalf("second projects refresh: %v", err)
	}

	rr = httptest.NewRecorder()
	srv.handleProjects(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &projects); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("expected refreshed index with two projects, got %+v", projects)
	}
}

func TestHandleSessions_Empty(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest("GET", "/api/sessions", nil)
	rr := httptest.NewRecorder()
	srv.handleSessions(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleSession_NotFound(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest("GET", "/api/session/nonexistent", nil)
	req.URL.Path = "/api/session/nonexistent"
	rr := httptest.NewRecorder()
	srv.handleSession(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleSession_InvalidID(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest("GET", "/api/session/invalid!id", nil)
	req.URL.Path = "/api/session/invalid!id"
	rr := httptest.NewRecorder()
	srv.handleSession(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestHandleActivity_ReturnsData(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest("GET", "/api/activity", nil)
	rr := httptest.NewRecorder()
	srv.handleActivity(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var activity []interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &activity); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	// Should return 366 entries (365 days + today, no filter)
	if len(activity) != 366 {
		t.Errorf("expected 366 daily entries, got %d", len(activity))
	}
}

func TestHandleHourly_Returns24Hours(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest("GET", "/api/hourly", nil)
	rr := httptest.NewRecorder()
	srv.handleHourly(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var hourly []interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &hourly); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(hourly) != 24 {
		t.Errorf("expected 24 hours, got %d", len(hourly))
	}
}

func TestHandleModels_Empty(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest("GET", "/api/models", nil)
	rr := httptest.NewRecorder()
	srv.handleModels(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestHandleSeenSession_InvalidID(t *testing.T) {
	srv := testServer(t)
	body := strings.NewReader(`{"sessionId":"invalid!","timeUpdated":1000}`)
	req := httptest.NewRequest("POST", "/api/session/seen", body)
	rr := httptest.NewRecorder()
	srv.handleSeenSession(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestHandleSeenSession_Valid(t *testing.T) {
	srv := testServer(t)
	body := strings.NewReader(`{"platform":"opencode","sessionId":"abc123","timeUpdated":1000}`)
	req := httptest.NewRequest("POST", "/api/session/seen", body)
	rr := httptest.NewRecorder()
	srv.handleSeenSession(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleArchiveSession_Valid(t *testing.T) {
	srv := testServer(t)
	body := strings.NewReader(`{"platform":"opencode","sessionId":"abc123","timeUpdated":1000,"archived":true}`)
	req := httptest.NewRequest("POST", "/api/session/archive", body)
	rr := httptest.NewRecorder()
	srv.handleArchiveSession(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleArchiveSession_Unarchive(t *testing.T) {
	srv := testServer(t)
	body := strings.NewReader(`{"platform":"opencode","sessionId":"abc123","archived":false}`)
	req := httptest.NewRequest("POST", "/api/session/archive", body)
	rr := httptest.NewRecorder()
	srv.handleArchiveSession(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleArchiveSession_MissingTimeUpdated(t *testing.T) {
	srv := testServer(t)
	body := strings.NewReader(`{"sessionId":"abc123","archived":true}`)
	req := httptest.NewRequest("POST", "/api/session/archive", body)
	rr := httptest.NewRecorder()
	srv.handleArchiveSession(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 (missing timeUpdated), got %d", rr.Code)
	}
}

func TestHandleArchiveSession_InvalidJSON(t *testing.T) {
	srv := testServer(t)
	body := strings.NewReader(`{invalid}`)
	req := httptest.NewRequest("POST", "/api/session/archive", body)
	rr := httptest.NewRecorder()
	srv.handleArchiveSession(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

// Note: the old /api/agents and /api/commands tests (and the
// primePortCache helper) have moved with their implementations into the
// internal/platforms/opencode package. The server layer now dispatches
// every session-scoped operation through the Platform adapter, so the
// proxy behaviour is covered by the adapter's own tests rather than the
// server's.

// --- applySessionState tests ---

func TestApplySessionState_MarksSeen(t *testing.T) {
	srv := testServer(t)
	if err := srv.stateDB.MarkSessionSeen("opencode", "s1", 2000); err != nil {
		t.Fatal(err)
	}

	sessions := []db.Session{
		{ID: "s1", Platform: "opencode", TimeUpdated: 2000},
		{ID: "s2", Platform: "opencode", TimeUpdated: 3000},
	}
	if err := srv.applySessionState(sessions); err != nil {
		t.Fatalf("applySessionState: %v", err)
	}
	if !sessions[0].Seen {
		t.Error("s1 should be marked as seen")
	}
	if sessions[1].Seen {
		t.Error("s2 should not be marked as seen")
	}
}

func TestApplySessionState_MarksArchived(t *testing.T) {
	srv := testServer(t)
	if err := srv.stateDB.ArchiveSession("opencode", "s1", 2000); err != nil {
		t.Fatal(err)
	}

	sessions := []db.Session{
		{ID: "s1", Platform: "opencode", TimeUpdated: 2000},
	}
	if err := srv.applySessionState(sessions); err != nil {
		t.Fatalf("applySessionState: %v", err)
	}
	if !sessions[0].Archived {
		t.Error("s1 should be archived")
	}
}

func TestApplySessionState_AutoUnarchivesUpdatedSession(t *testing.T) {
	srv := testServer(t)
	if err := srv.stateDB.ArchiveSession("opencode", "s1", 1000); err != nil {
		t.Fatal(err)
	}

	sessions := []db.Session{
		{ID: "s1", Platform: "opencode", TimeUpdated: 2000},
	}
	if err := srv.applySessionState(sessions); err != nil {
		t.Fatalf("applySessionState: %v", err)
	}
	if sessions[0].Archived {
		t.Error("s1 should have been auto-unarchived (updated since archive)")
	}

	archived, _ := srv.stateDB.ArchivedSessions()
	if _, ok := archived[state.Key{Platform: "opencode", SessionID: "s1"}]; ok {
		t.Error("s1 should no longer be in archived_session table")
	}
}

// TestApplySessionState_ScopesByPlatform verifies that marking a
// session seen / archived under one platform has no effect on a
// session with the same ID under a different platform — the
// multi-platform point of the state.db migration (AD-10).
func TestApplySessionState_ScopesByPlatform(t *testing.T) {
	srv := testServer(t)
	if err := srv.stateDB.ArchiveSession("opencode", "shared-id", 1000); err != nil {
		t.Fatal(err)
	}

	sessions := []db.Session{
		{ID: "shared-id", Platform: "opencode", TimeUpdated: 1000},
		{ID: "shared-id", Platform: "claude-code", TimeUpdated: 1000},
	}
	if err := srv.applySessionState(sessions); err != nil {
		t.Fatalf("applySessionState: %v", err)
	}
	if !sessions[0].Archived {
		t.Error("opencode/shared-id should be archived")
	}
	if sessions[1].Archived {
		t.Error("claude-code/shared-id must NOT inherit opencode's archive state")
	}
}

// --- graceful shutdown test ---

func TestServerStart_ShutdownOnCancel(t *testing.T) {
	srv := testServer(t)
	srv.addr = "127.0.0.1:0" // random port

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start(ctx)
	}()

	// Cancel immediately
	cancel()

	err := <-errCh
	if err != nil {
		t.Fatalf("expected nil error on clean shutdown, got: %v", err)
	}
}

// TestAutoArchive_UsesRegistry verifies the auto-archive pass iterates
// registered agents (as opposed to calling s.db directly) so that in
// future phases Claude Code sessions will be picked up for the same
// archive logic.
func TestAutoArchive_UsesRegistry(t *testing.T) {
	srv, rawDB := testServerWithRawDB(t)
	defer rawDB.Close()

	// Seed one old session (time_updated in 1970) so it qualifies as stale
	// under the auto-archive window.
	_, err := rawDB.Exec(
		`INSERT INTO session (id, title, directory, time_created, time_updated)
		 VALUES ('old-session', 'ancient', '/tmp', 1000, 1000)`,
	)
	if err != nil {
		t.Fatalf("seeding session: %v", err)
	}

	srv.autoArchiveInactiveSessions()

	archived, err := srv.stateDB.ArchivedSessions()
	if err != nil {
		t.Fatalf("ArchivedSessions: %v", err)
	}
	if _, ok := archived[state.Key{Platform: "opencode", SessionID: "old-session"}]; !ok {
		t.Errorf("expected old-session to be archived under opencode after auto-archive pass, got %+v", archived)
	}
}

// testServerWithRawDB is like testServer but also returns a raw *sql.DB
// handle to the same OpenCode database so tests can seed additional rows.
func testServerWithRawDB(t *testing.T) (*Server, *sql.DB) {
	t.Helper()
	tmpDir := t.TempDir()

	tmpOC := tmpDir + "/opencode.db"
	setupDB, err := sql.Open("sqlite3", tmpOC)
	if err != nil {
		t.Fatalf("opening temp oc file: %v", err)
	}
	_, err = setupDB.Exec(`
		CREATE TABLE session (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL DEFAULT '',
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
		setupDB.Close()
		t.Fatalf("creating oc schema: %v", err)
	}

	database, err := db.OpenReadWrite(tmpOC)
	if err != nil {
		setupDB.Close()
		t.Fatalf("opening db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	stateDB, err := state.Open(tmpDir + "/state.db")
	if err != nil {
		setupDB.Close()
		t.Fatalf("opening state db: %v", err)
	}
	t.Cleanup(func() { stateDB.Close() })

	reg := platforms.NewRegistry()
	reg.Register(opencodeplatform.New(database, stateDB))
	srv := New(database, stateDB, "127.0.0.1:0", reg, nil)
	return srv, setupDB
}

// TestHandleSessions_MergesByTimeUpdatedDesc guards the list merge
// order across platforms. Each adapter returns its own sessions already
// sorted by TimeUpdated desc, but the combined /api/sessions response
// must also interleave across platforms so a recent Claude Code
// session is not hidden behind hundreds of older OpenCode rows.
//
// Regression: prior to the sort.SliceStable in handleSessions the
// combined slice was a naive concatenation, which placed every
// claude-code row after every opencode row regardless of recency.
func TestHandleSessions_MergesByTimeUpdatedDesc(t *testing.T) {
	srv, rawDB := testServerWithRawDB(t)
	defer rawDB.Close()

	// Register a second (fake) platform alongside the real OpenCode
	// adapter already registered by testServerWithRawDB. The fake
	// returns two sessions: one newer and one older than the OpenCode
	// rows we seed below, proving interleaving — not tail-append.
	//
	// Timestamps are spaced >5 min apart so each falls in its own
	// 5-minute bucket, which is the primary sort key.
	const minute = int64(60 * 1000)
	srv.registry.Register(&fakePlatform{
		id: "claude-code",
		sessions: []db.Session{
			{ID: "cc-newest", Platform: "claude-code", TimeUpdated: 40 * minute, Directory: "/tmp"},
			{ID: "cc-oldest", Platform: "claude-code", TimeUpdated: 5 * minute, Directory: "/tmp"},
		},
	})

	// Seed two opencode sessions with timestamps that bracket the
	// claude-code rows. The expected sorted order is:
	//   cc-newest (40min) > oc-mid (30min) > oc-old (15min) > cc-oldest (5min)
	// which is only achievable if the handler sorts the union.
	_, err := rawDB.Exec(
		`INSERT INTO session (id, title, directory, time_created, time_updated)
		 VALUES ('oc-mid', 'mid', '/tmp', 1800000, 1800000),
		        ('oc-old', 'old', '/tmp', 900000, 900000)`,
	)
	if err != nil {
		t.Fatalf("seeding opencode sessions: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/sessions", nil)
	rr := httptest.NewRecorder()
	srv.handleSessions(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var got []db.Session
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 sessions, got %d: %+v", len(got), got)
	}
	wantIDs := []string{"cc-newest", "oc-mid", "oc-old", "cc-oldest"}
	for i, want := range wantIDs {
		if got[i].ID != want {
			t.Errorf("position %d: got %s, want %s (full order: %v)",
				i, got[i].ID, want,
				func() []string {
					ids := make([]string, len(got))
					for j, s := range got {
						ids[j] = s.ID
					}
					return ids
				}())
		}
	}
}

// --- /api/session/{id}/changes ---

// TestHandleSessionChanges_OpenCodeAggregates exercises the happy
// path: an OpenCode session with two edits to a single file is
// aggregated into one FileChange with the right counts and snapshots.
func TestHandleSessionChanges_OpenCodeAggregates(t *testing.T) {
	srv, rawDB := testServerWithRawDB(t)
	defer rawDB.Close()

	_, err := rawDB.Exec(
		`INSERT INTO session (id, title, directory, time_created, time_updated)
		 VALUES ('s1', 't', '/work', 1, 1)`,
	)
	if err != nil {
		t.Fatalf("seeding session: %v", err)
	}
	_, err = rawDB.Exec(
		`INSERT INTO message (id, session_id, time_created, data)
		 VALUES ('m1', 's1', 1, '{"role":"assistant"}')`,
	)
	if err != nil {
		t.Fatalf("seeding message: %v", err)
	}
	// Two edits to the same file, with filediff metadata.
	editPartJSON := func(file, before, after string, adds, dels int) string {
		raw, err := json.Marshal(map[string]any{
			"type": "tool",
			"tool": "edit",
			"state": map[string]any{
				"input": map[string]any{
					"filePath":  file,
					"oldString": before,
					"newString": after,
				},
				"metadata": map[string]any{
					"filediff": map[string]any{
						"file": file, "before": before, "after": after,
						"additions": adds, "deletions": dels,
					},
				},
			},
		})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return string(raw)
	}
	_, err = rawDB.Exec(
		`INSERT INTO part (id, message_id, session_id, time_created, data)
		 VALUES (?, ?, ?, ?, ?), (?, ?, ?, ?, ?)`,
		"p1", "m1", "s1", 100, editPartJSON("/work/hero.tsx", "v0", "v1", 2, 1),
		"p2", "m1", "s1", 200, editPartJSON("/work/hero.tsx", "v1", "v2", 3, 0),
	)
	if err != nil {
		t.Fatalf("seeding parts: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/session/s1/changes", nil)
	req.URL.Path = "/api/session/s1/changes"
	rr := httptest.NewRecorder()
	srv.handleSessionChanges(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var got platforms.SessionChanges
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, rr.Body.String())
	}
	if !got.Supported {
		t.Errorf("Supported should be true for OpenCode")
	}
	if got.FilesChanged != 1 {
		t.Fatalf("FilesChanged = %d, want 1", got.FilesChanged)
	}
	if got.TotalAdditions != 5 || got.TotalDeletions != 1 {
		t.Errorf("totals = %d/%d, want 5/1", got.TotalAdditions, got.TotalDeletions)
	}
	f := got.Files[0]
	if f.DisplayPath != "hero.tsx" {
		t.Errorf("DisplayPath = %q, want hero.tsx", f.DisplayPath)
	}
	if f.Before != "v0" || f.After != "v2" {
		t.Errorf("first-before/last-after = %q/%q, want v0/v2", f.Before, f.After)
	}
	if f.EditCount != 2 || len(f.Edits) != 2 {
		t.Errorf("EditCount = %d, len(Edits) = %d", f.EditCount, len(f.Edits))
	}
}

// TestHandleSessionChanges_UnsupportedReturns200 verifies that
// adapters that report ErrUnsupported (Claude Code) yield an HTTP 200
// response with Supported=false rather than an HTTP error, so the
// frontend has a single shape to handle.
func TestHandleSessionChanges_UnsupportedReturns200(t *testing.T) {
	srv, rawDB := testServerWithRawDB(t)
	defer rawDB.Close()

	fakeSessions := []db.Session{
		{ID: "fk-1", Platform: "fake", Directory: "/x", TimeUpdated: 1},
	}
	srv.registry.Register(&fakePlatform{id: "fake", sessions: fakeSessions})
	// Pre-populate the reverse-lookup cache so resolvePlatformForSession
	// finds the fake adapter without falling back to Session() (the fake
	// returns ErrNotFound there).
	srv.registry.RememberSessions("fake", fakeSessions)

	req := httptest.NewRequest("GET", "/api/session/fk-1/changes", nil)
	req.URL.Path = "/api/session/fk-1/changes"
	rr := httptest.NewRecorder()
	srv.handleSessionChanges(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for unsupported, got %d: %s", rr.Code, rr.Body.String())
	}
	var got platforms.SessionChanges
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got.Supported {
		t.Errorf("Supported should be false for fake platform")
	}
	if got.Files == nil {
		t.Errorf("Files should be empty slice (so JSON renders []), not nil")
	}
}
