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

	return New(database, stateDB, "127.0.0.1:0")
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
	body := strings.NewReader(`{"sessionId":"abc123","timeUpdated":1000}`)
	req := httptest.NewRequest("POST", "/api/session/seen", body)
	rr := httptest.NewRecorder()
	srv.handleSeenSession(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleArchiveSession_Valid(t *testing.T) {
	srv := testServer(t)
	body := strings.NewReader(`{"sessionId":"abc123","timeUpdated":1000,"archived":true}`)
	req := httptest.NewRequest("POST", "/api/session/archive", body)
	rr := httptest.NewRecorder()
	srv.handleArchiveSession(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleArchiveSession_Unarchive(t *testing.T) {
	srv := testServer(t)
	body := strings.NewReader(`{"sessionId":"abc123","archived":false}`)
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

func TestHandleAbortSession_NoPort(t *testing.T) {
	srv := testServer(t)
	body := strings.NewReader(`{"sessionId":"abc123","directory":"/nonexistent"}`)
	req := httptest.NewRequest("POST", "/api/abort-session", body)
	rr := httptest.NewRecorder()
	srv.handleAbortSession(rr, req)

	// No OpenCode instance running, so should get 503
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rr.Code)
	}
}

func TestHandleSessionPort_InvalidID(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest("GET", "/api/session-port/bad!id", nil)
	req.URL.Path = "/api/session-port/bad!id"
	rr := httptest.NewRecorder()
	srv.handleSessionPort(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

// --- applySessionState tests ---

func TestApplySessionState_MarksSeen(t *testing.T) {
	srv := testServer(t)
	// Mark s1 as seen at time 2000
	if err := srv.stateDB.MarkSessionSeen("s1", 2000); err != nil {
		t.Fatal(err)
	}

	sessions := []db.Session{
		{ID: "s1", TimeUpdated: 2000},
		{ID: "s2", TimeUpdated: 3000},
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
	if err := srv.stateDB.ArchiveSession("s1", 2000); err != nil {
		t.Fatal(err)
	}

	sessions := []db.Session{
		{ID: "s1", TimeUpdated: 2000},
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
	if err := srv.stateDB.ArchiveSession("s1", 1000); err != nil {
		t.Fatal(err)
	}

	// Session was updated after archive
	sessions := []db.Session{
		{ID: "s1", TimeUpdated: 2000},
	}
	if err := srv.applySessionState(sessions); err != nil {
		t.Fatalf("applySessionState: %v", err)
	}
	if sessions[0].Archived {
		t.Error("s1 should have been auto-unarchived (updated since archive)")
	}

	// Verify it was actually removed from the DB
	archived, _ := srv.stateDB.ArchivedSessions()
	if _, ok := archived["s1"]; ok {
		t.Error("s1 should no longer be in archived_session table")
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
