package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

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
	setupDB, err := sql.Open("sqlite", tmpOC)
	if err != nil {
		t.Fatalf("opening temp oc file: %v", err)
	}
	_, err = setupDB.Exec(`
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

func TestHandleCreateSession_RefreshesProjectsIndex(t *testing.T) {
	srv, rawDB := testServerWithRawDB(t)
	defer rawDB.Close()

	reg := platforms.NewRegistry()
	reg.Register(&fakePlatform{
		id: "fake",
		createSessionFn: func(req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
			_, err := rawDB.Exec(
				`INSERT INTO session (id, title, directory, time_created, time_updated)
				 VALUES ('new-session', 'new', ?, 3000, 3000)`,
				req.Directory,
			)
			if err != nil {
				return nil, err
			}
			return &platforms.CreateSessionResponse{ID: "new-session"}, nil
		},
	})
	srv.registry = reg

	if err := srv.refreshProjectsIndex(); err != nil {
		t.Fatalf("initial projects refresh: %v", err)
	}

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/sessions",
		strings.NewReader(`{"platform":"fake","directory":"/repo/new"}`),
	)
	rr := httptest.NewRecorder()
	srv.handleCreateSession(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	projects, _ := srv.projectsSnapshot()
	if len(projects) != 1 || projects[0].Directory != "/repo/new" {
		t.Fatalf("expected projects index to include created project, got %+v", projects)
	}
}

func TestHandleFilesystemDirectories_ReturnsDirectoriesOnly(t *testing.T) {
	srv := testServer(t)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "alpha"), 0o755); err != nil {
		t.Fatalf("mkdir alpha: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "beta"), 0o755); err != nil {
		t.Fatalf("mkdir beta: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, ".hidden"), 0o755); err != nil {
		t.Fatalf("mkdir hidden: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/filesystem/directories?dir="+url.QueryEscape(root), nil)
	rr := httptest.NewRecorder()
	srv.handleFilesystemDirectories(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp directoryBrowseResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}
	if resp.Directory != filepath.Clean(root) {
		t.Fatalf("directory = %q, want %q", resp.Directory, filepath.Clean(root))
	}
	if resp.Parent != filepath.Dir(root) {
		t.Fatalf("parent = %q, want %q", resp.Parent, filepath.Dir(root))
	}
	names := make([]string, 0, len(resp.Entries))
	for _, entry := range resp.Entries {
		names = append(names, entry.Name)
		if !filepath.IsAbs(entry.Path) {
			t.Fatalf("entry path %q is not absolute", entry.Path)
		}
	}
	if strings.Join(names, ",") != "alpha,beta,.hidden" {
		t.Fatalf("entries = %v, want alpha,beta,.hidden", names)
	}
	if len(resp.Entries) != 3 || !resp.Entries[2].Hidden {
		t.Fatalf("hidden entry flag not set: %+v", resp.Entries)
	}
}

func TestHandleFilesystemDirectories_RejectsRelativePath(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest("GET", "/api/filesystem/directories?dir=relative", nil)
	rr := httptest.NewRecorder()
	srv.handleFilesystemDirectories(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleFilesystemDirectorySearch_FindsNestedProjectDirectories(t *testing.T) {
	srv := testServer(t)
	root := t.TempDir()
	projectDir := filepath.Join(root, "workspace", "ocman")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "go.mod"), []byte("module example.com/ocman\n"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "node_modules", "ocman-copy"), 0o755); err != nil {
		t.Fatalf("mkdir skipped dir: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/filesystem/directory-search?root="+url.QueryEscape(root)+"&q=work+oc&limit=10", nil)
	rr := httptest.NewRecorder()
	srv.handleFilesystemDirectorySearch(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp directorySearchResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}
	if resp.Root != filepath.Clean(root) {
		t.Fatalf("root = %q, want %q", resp.Root, filepath.Clean(root))
	}
	if len(resp.Entries) != 1 {
		t.Fatalf("entries = %+v, want one project match", resp.Entries)
	}
	if resp.Entries[0].Path != projectDir || !resp.Entries[0].Project {
		t.Fatalf("entry = %+v, want project dir %q", resp.Entries[0], projectDir)
	}
}

func TestHandleFilesystemDirectorySearch_DeduplicatesExactAbsoluteDirectory(t *testing.T) {
	srv := testServer(t)
	root := t.TempDir()
	workspaceDir := filepath.Join(root, "workspace")
	projectDir := filepath.Join(workspaceDir, "research")
	nestedDir := filepath.Join(projectDir, "anam-research")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatalf("mkdir nested project: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "go.mod"), []byte("module example.com/research\n"), 0o644); err != nil {
		t.Fatalf("write project marker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nestedDir, "package.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write nested project marker: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/filesystem/directory-search?root="+url.QueryEscape(root)+"&q="+url.QueryEscape(projectDir)+"&limit=10", nil)
	rr := httptest.NewRecorder()
	srv.handleFilesystemDirectorySearch(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp directorySearchResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}
	if resp.Root != filepath.Clean(workspaceDir) {
		t.Fatalf("root = %q, want %q", resp.Root, filepath.Clean(workspaceDir))
	}

	projectCount := 0
	nestedFound := false
	for _, entry := range resp.Entries {
		switch entry.Path {
		case projectDir:
			projectCount++
		case nestedDir:
			nestedFound = true
		}
	}
	if projectCount != 1 {
		t.Fatalf("project path appears %d times in %+v, want once", projectCount, resp.Entries)
	}
	if !nestedFound {
		t.Fatalf("nested project %q not found in %+v", nestedDir, resp.Entries)
	}
}

func TestHandleFilesystemDirectorySearch_RejectsRelativeRoot(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest("GET", "/api/filesystem/directory-search?root=relative&q=oc", nil)
	rr := httptest.NewRecorder()
	srv.handleFilesystemDirectorySearch(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
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

// TestApplySessionState_OverlaysMCPChildParent verifies that a session
// spawned via the MCP split tools (tracked only in state.db's
// child_sessions table) gets its ParentID populated, and that an
// existing platform-supplied ParentID (an OpenCode subagent) is never
// overwritten by the overlay.
func TestApplySessionState_OverlaysMCPChildParent(t *testing.T) {
	srv := testServer(t)
	if err := srv.stateDB.InsertChildSession(state.ChildSession{
		ID:              "child-mcp",
		Platform:        "opencode",
		ParentSessionID: "parent-mcp",
		Intent:          "do a thing",
		ComposedPrompt:  "prompt",
		Status:          "running",
		CreatedAt:       1000,
	}); err != nil {
		t.Fatalf("InsertChildSession: %v", err)
	}

	sessions := []db.Session{
		{ID: "parent-mcp", Platform: "opencode", TimeUpdated: 2000},
		{ID: "child-mcp", Platform: "opencode", TimeUpdated: 2000},
		// A subagent whose parent_id already came from the platform;
		// the overlay must leave it untouched even if a stale
		// child_sessions row ever pointed elsewhere.
		{ID: "subagent", Platform: "opencode", ParentID: "platform-parent", TimeUpdated: 2000},
	}
	if err := srv.applySessionState(sessions); err != nil {
		t.Fatalf("applySessionState: %v", err)
	}
	if sessions[1].ParentID != "parent-mcp" {
		t.Errorf("child-mcp ParentID = %q, want %q", sessions[1].ParentID, "parent-mcp")
	}
	if sessions[0].ParentID != "" {
		t.Errorf("parent-mcp should stay top-level, got ParentID %q", sessions[0].ParentID)
	}
	if sessions[2].ParentID != "platform-parent" {
		t.Errorf("platform-supplied ParentID overwritten: got %q", sessions[2].ParentID)
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

// TestApplySessionState_SeenTimeUpdated verifies that the user's
// last-seen cutoff for a session is surfaced on the Session struct
// even when the session has since received new updates (so .Seen is
// false but the frontend still needs the cutoff to render a
// "jump to first unread" marker).
func TestApplySessionState_SeenTimeUpdated(t *testing.T) {
	srv := testServer(t)
	if err := srv.stateDB.MarkSessionSeen("opencode", "s1", 1500); err != nil {
		t.Fatal(err)
	}

	sessions := []db.Session{
		{ID: "s1", Platform: "opencode", TimeUpdated: 2500}, // updated since seen
		{ID: "s2", Platform: "opencode", TimeUpdated: 3000}, // never seen
	}
	if err := srv.applySessionState(sessions); err != nil {
		t.Fatalf("applySessionState: %v", err)
	}
	if sessions[0].SeenTimeUpdated != 1500 {
		t.Errorf("s1 SeenTimeUpdated: want 1500, got %d", sessions[0].SeenTimeUpdated)
	}
	if sessions[0].Seen {
		t.Error("s1 should not be marked .Seen (updated since)")
	}
	if sessions[1].SeenTimeUpdated != 0 {
		t.Errorf("s2 (never seen) SeenTimeUpdated: want 0, got %d", sessions[1].SeenTimeUpdated)
	}
}

// TestApplySessionState_UnreadCount inserts messages into the
// fake OpenCode DB so the adapter's UnreadCounter implementation
// has real rows to count against. Verifies the overlay correctly
// surfaces unread counts on the Session struct.
func TestApplySessionState_UnreadCount(t *testing.T) {
	srv, rawDB := testServerWithRawDB(t)
	defer rawDB.Close()

	// Seed the OpenCode DB with two sessions and a few messages.
	mustExec := func(q string, args ...interface{}) {
		t.Helper()
		if _, err := rawDB.Exec(q, args...); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	mustExec(`INSERT INTO session (id, title, directory, time_created, time_updated) VALUES ('s1', 'S1', '/d', 100, 400)`)
	mustExec(`INSERT INTO session (id, title, directory, time_created, time_updated) VALUES ('s2', 'S2', '/d', 100, 300)`)
	mustExec(`INSERT INTO message (id, session_id, time_created, data) VALUES ('m1', 's1', 100, '{}')`)
	mustExec(`INSERT INTO message (id, session_id, time_created, data) VALUES ('m2', 's1', 250, '{}')`)
	mustExec(`INSERT INTO message (id, session_id, time_created, data) VALUES ('m3', 's1', 400, '{}')`)
	mustExec(`INSERT INTO message (id, session_id, time_created, data) VALUES ('m4', 's2', 150, '{}')`)
	mustExec(`INSERT INTO message (id, session_id, time_created, data) VALUES ('m5', 's2', 300, '{}')`)

	// s1: user saw up to time_updated=200 → 2 messages newer (m2, m3).
	// s2: user saw up to time_updated=300 → fully seen, count omitted.
	if err := srv.stateDB.MarkSessionSeen("opencode", "s1", 200); err != nil {
		t.Fatal(err)
	}
	if err := srv.stateDB.MarkSessionSeen("opencode", "s2", 300); err != nil {
		t.Fatal(err)
	}

	sessions := []db.Session{
		{ID: "s1", Platform: "opencode", TimeUpdated: 400},
		{ID: "s2", Platform: "opencode", TimeUpdated: 300},
	}
	if err := srv.applySessionState(sessions); err != nil {
		t.Fatalf("applySessionState: %v", err)
	}
	if sessions[0].UnreadCount != 2 {
		t.Errorf("s1 UnreadCount: want 2, got %d", sessions[0].UnreadCount)
	}
	if sessions[1].UnreadCount != 0 {
		t.Errorf("s2 UnreadCount: want 0 (fully seen), got %d", sessions[1].UnreadCount)
	}
	if !sessions[1].Seen {
		t.Error("s2 should be marked .Seen")
	}
}

// TestApplySessionState_UnreadCount_NeverSeen verifies that a
// session the user has never opened reports all its messages as
// unread (cutoff defaults to 0).
func TestApplySessionState_UnreadCount_NeverSeen(t *testing.T) {
	srv, rawDB := testServerWithRawDB(t)
	defer rawDB.Close()

	mustExec := func(q string, args ...interface{}) {
		t.Helper()
		if _, err := rawDB.Exec(q, args...); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	mustExec(`INSERT INTO session (id, title, directory, time_created, time_updated) VALUES ('s1', 'S1', '/d', 100, 500)`)
	mustExec(`INSERT INTO message (id, session_id, time_created, data) VALUES ('m1', 's1', 100, '{}')`)
	mustExec(`INSERT INTO message (id, session_id, time_created, data) VALUES ('m2', 's1', 200, '{}')`)
	mustExec(`INSERT INTO message (id, session_id, time_created, data) VALUES ('m3', 's1', 300, '{}')`)

	sessions := []db.Session{
		{ID: "s1", Platform: "opencode", TimeUpdated: 500},
	}
	if err := srv.applySessionState(sessions); err != nil {
		t.Fatalf("applySessionState: %v", err)
	}
	if sessions[0].UnreadCount != 3 {
		t.Errorf("never-seen UnreadCount: want 3, got %d", sessions[0].UnreadCount)
	}
	if sessions[0].SeenTimeUpdated != 0 {
		t.Errorf("never-seen SeenTimeUpdated: want 0, got %d", sessions[0].SeenTimeUpdated)
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
		{ID: "shared-id", Platform: "other-platform", TimeUpdated: 1000},
	}
	if err := srv.applySessionState(sessions); err != nil {
		t.Fatalf("applySessionState: %v", err)
	}
	if !sessions[0].Archived {
		t.Error("opencode/shared-id should be archived")
	}
	if sessions[1].Archived {
		t.Error("other-platform/shared-id must NOT inherit opencode's archive state")
	}
}

// --- Pin handler tests ---

func TestHandlePinSession_Pin(t *testing.T) {
	srv := testServer(t)
	body := strings.NewReader(`{"platform":"opencode","sessionId":"abc123","pinned":true}`)
	req := httptest.NewRequest("POST", "/api/session/pin", body)
	rr := httptest.NewRecorder()
	srv.handlePinSession(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	pinned, _ := srv.stateDB.PinnedSessions()
	if _, ok := pinned[state.Key{Platform: "opencode", SessionID: "abc123"}]; !ok {
		t.Error("session should be pinned after POST")
	}
}

func TestHandlePinSession_Unpin(t *testing.T) {
	srv := testServer(t)
	_ = srv.stateDB.PinSession("opencode", "abc123")

	body := strings.NewReader(`{"platform":"opencode","sessionId":"abc123","pinned":false}`)
	req := httptest.NewRequest("POST", "/api/session/pin", body)
	rr := httptest.NewRecorder()
	srv.handlePinSession(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	pinned, _ := srv.stateDB.PinnedSessions()
	if _, ok := pinned[state.Key{Platform: "opencode", SessionID: "abc123"}]; ok {
		t.Error("session should be unpinned after POST")
	}
}

func TestHandlePinSession_InvalidID(t *testing.T) {
	srv := testServer(t)
	body := strings.NewReader(`{"platform":"opencode","sessionId":"invalid!","pinned":true}`)
	req := httptest.NewRequest("POST", "/api/session/pin", body)
	rr := httptest.NewRecorder()
	srv.handlePinSession(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestHandlePinSession_InvalidJSON(t *testing.T) {
	srv := testServer(t)
	body := strings.NewReader(`{invalid}`)
	req := httptest.NewRequest("POST", "/api/session/pin", body)
	rr := httptest.NewRecorder()
	srv.handlePinSession(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

// --- applySessionState pinned overlay ---

func TestApplySessionState_MarksPinned(t *testing.T) {
	srv := testServer(t)
	if err := srv.stateDB.PinSession("opencode", "s1"); err != nil {
		t.Fatal(err)
	}

	sessions := []db.Session{
		{ID: "s1", Platform: "opencode", TimeUpdated: 2000},
		{ID: "s2", Platform: "opencode", TimeUpdated: 3000},
	}
	if err := srv.applySessionState(sessions); err != nil {
		t.Fatalf("applySessionState: %v", err)
	}
	if !sessions[0].Pinned {
		t.Error("s1 should be marked as pinned")
	}
	if sessions[0].PinnedAt <= 0 {
		t.Error("s1 should have a positive PinnedAt")
	}
	if sessions[1].Pinned {
		t.Error("s2 should not be marked as pinned")
	}
}

func TestApplySessionState_PinnedScopesByPlatform(t *testing.T) {
	srv := testServer(t)
	if err := srv.stateDB.PinSession("opencode", "shared-id"); err != nil {
		t.Fatal(err)
	}

	sessions := []db.Session{
		{ID: "shared-id", Platform: "opencode", TimeUpdated: 1000},
		{ID: "shared-id", Platform: "other-platform", TimeUpdated: 1000},
	}
	if err := srv.applySessionState(sessions); err != nil {
		t.Fatalf("applySessionState: %v", err)
	}
	if !sessions[0].Pinned {
		t.Error("opencode/shared-id should be pinned")
	}
	if sessions[1].Pinned {
		t.Error("other-platform/shared-id must NOT inherit opencode's pin state")
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
	// The opencode package keeps several process-global caches
	// (recents, session defaults, sessions list) for production
	// performance. Tests that seed a fresh DB need a fresh cache,
	// otherwise a row inserted after a previous test's GetSessions
	// call won't appear in this test's result. Reset before doing
	// anything else so the cache state matches the DB state.
	opencodeplatform.ResetCachesForTests()
	tmpDir := t.TempDir()

	tmpOC := tmpDir + "/opencode.db"
	setupDB, err := sql.Open("sqlite", tmpOC)
	if err != nil {
		t.Fatalf("opening temp oc file: %v", err)
	}
	_, err = setupDB.Exec(`
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
// must also interleave across platforms so a recent session from a
// second platform is not hidden behind hundreds of older OpenCode rows.
//
// Regression: prior to the sort.SliceStable in handleSessions the
// combined slice was a naive concatenation, which placed every
// secondary-platform row after every opencode row regardless of recency.
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
		id: "other-platform",
		sessions: []db.Session{
			{ID: "cc-newest", Platform: "other-platform", TimeUpdated: 40 * minute, Directory: "/tmp"},
			{ID: "cc-oldest", Platform: "other-platform", TimeUpdated: 5 * minute, Directory: "/tmp"},
		},
	})

	// Seed two opencode sessions with timestamps that bracket the
	// other-platform rows. The expected sorted order is:
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
	wantIDs := []string{"cc-newest", "oc-mid", "oc-old", "cc-oldest"} //nolint:gocritic // IDs are stable test fixtures
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

// --- /api/session/{id}/info ---

// TestHandleSessionInfo_PlatformPayload verifies the happy path: an
// adapter returning a populated SessionInfo is forwarded verbatim with
// Supported=true, MCP/LSP slices preserved, context numbers intact.
// Uses the fake platform so we exercise the handler without standing
// up an actual OpenCode instance.
func TestHandleSessionInfo_PlatformPayload(t *testing.T) {
	srv, rawDB := testServerWithRawDB(t)
	defer rawDB.Close()

	fakeSessions := []db.Session{
		{ID: "fk-1", Platform: "fake", Directory: "/x", TimeUpdated: 1},
	}
	want := &platforms.SessionInfo{
		SessionID: "fk-1",
		Supported: true,
		Context: platforms.ContextInfo{
			Tokens: 153_309,
			Limit:  200_000,
			Cost:   0.42,
			Model:  "anthropic/claude-sonnet-4",
		},
		MCPServers: []platforms.MCPServer{
			{Name: "devtoys", Status: "needs_auth"},
			{Name: "weave", Status: "failed", Error: "Failed to get tools"},
		},
		LSPServers: []platforms.LSPServer{
			{ID: "gopls", Name: "gopls", Status: "connected"},
		},
	}
	srv.registry.Register(&fakePlatform{id: "fake", sessions: fakeSessions, info: want})
	srv.registry.RememberSessions("fake", fakeSessions)

	req := httptest.NewRequest("GET", "/api/session/fk-1/info", nil)
	req.URL.Path = "/api/session/fk-1/info"
	rr := httptest.NewRecorder()
	srv.handleSessionInfo(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var got platforms.SessionInfo
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, rr.Body.String())
	}
	if !got.Supported {
		t.Errorf("Supported should be true")
	}
	if got.Context.Tokens != 153_309 || got.Context.Limit != 200_000 {
		t.Errorf("context tokens/limit = %d/%d, want 153309/200000", got.Context.Tokens, got.Context.Limit)
	}
	if got.Context.Cost != 0.42 {
		t.Errorf("context cost = %v, want 0.42", got.Context.Cost)
	}
	if got.Context.Model != "anthropic/claude-sonnet-4" {
		t.Errorf("context model = %q", got.Context.Model)
	}
	if len(got.MCPServers) != 2 {
		t.Fatalf("MCPServers len = %d, want 2", len(got.MCPServers))
	}
	if len(got.LSPServers) != 1 || got.LSPServers[0].ID != "gopls" {
		t.Errorf("LSPServers = %+v", got.LSPServers)
	}
}

// TestHandleSessionInfo_UnsupportedReturns200 verifies that adapters
// reporting ErrUnsupported (Claude Code, or OpenCode without a live
// port) yield an HTTP 200 with Supported=false and non-nil empty
// slices, mirroring the SessionChanges contract so the frontend has
// one shape to render.
func TestHandleSessionInfo_UnsupportedReturns200(t *testing.T) {
	srv, rawDB := testServerWithRawDB(t)
	defer rawDB.Close()

	fakeSessions := []db.Session{
		{ID: "fk-1", Platform: "fake", Directory: "/x", TimeUpdated: 1},
	}
	srv.registry.Register(&fakePlatform{id: "fake", sessions: fakeSessions})
	srv.registry.RememberSessions("fake", fakeSessions)

	req := httptest.NewRequest("GET", "/api/session/fk-1/info", nil)
	req.URL.Path = "/api/session/fk-1/info"
	rr := httptest.NewRecorder()
	srv.handleSessionInfo(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for unsupported, got %d: %s", rr.Code, rr.Body.String())
	}
	var got platforms.SessionInfo
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got.Supported {
		t.Errorf("Supported should be false for fake platform")
	}
	if got.MCPServers == nil {
		t.Errorf("MCPServers should be empty slice (not nil) so JSON renders []")
	}
	if got.LSPServers == nil {
		t.Errorf("LSPServers should be empty slice (not nil) so JSON renders []")
	}
}

// --- /api/sessions/notify ---

// TestHandleSessionsNotify_IncludesTitleAndDirectory verifies that the
// notify projection carries enough per-session context for the in-app
// toast notifier to render a useful "session needs input" message
// without needing a second round-trip to /api/sessions.
//
// Specifically: a session with a pending question must surface its
// title and directory so the toast can show "plan: refactor auth
// (/repo/foo)" with a deep link.
func TestHandleSessionsNotify_IncludesTitleAndDirectory(t *testing.T) {
	srv := testServer(t)

	srv.registry.Register(&fakePlatform{
		id: "fake",
		sessions: []db.Session{
			{
				ID:              "fk-prompt",
				Platform:        "fake",
				Title:           "Refactor auth flow",
				Directory:       "/repo/foo",
				TimeUpdated:     1000,
				Status:          "busy",
				PendingQuestion: true,
			},
			// A non-prompt, non-terminal session that should be filtered out
			// by the existing rules — confirms the new fields don't change
			// the filter behaviour.
			{
				ID:          "fk-busy",
				Platform:    "fake",
				Title:       "boring",
				Directory:   "/repo/bar",
				TimeUpdated: 1000,
				Status:      "busy",
			},
		},
	})

	req := httptest.NewRequest("GET", "/api/sessions/notify", nil)
	rr := httptest.NewRecorder()
	srv.handleSessionsNotify(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var got []notifyEntry
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, rr.Body.String())
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 notify entry (the prompt), got %d: %+v", len(got), got)
	}
	e := got[0]
	if e.ID != "fk-prompt" {
		t.Errorf("ID = %q, want %q", e.ID, "fk-prompt")
	}
	if !e.PendingQuestion {
		t.Errorf("PendingQuestion should be true")
	}
	if e.Title != "Refactor auth flow" {
		t.Errorf("Title = %q, want %q", e.Title, "Refactor auth flow")
	}
	if e.Directory != "/repo/foo" {
		t.Errorf("Directory = %q, want %q", e.Directory, "/repo/foo")
	}
}
