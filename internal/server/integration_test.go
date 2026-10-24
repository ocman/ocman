package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	hostlocal "github.com/NoUseFreak/ocman/internal/hostsvc/local"
	"github.com/NoUseFreak/ocman/internal/platforms"
	opencodeplatform "github.com/NoUseFreak/ocman/internal/platforms/opencode/provider"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

// Ensure the sql import is used (needed for the sqlite3 driver side-effect
// to work alongside the explicit mattn import).
var _ = sql.ErrNoRows
var _ = json.Unmarshal

// testServer creates a Server backed by file-backed SQLite databases and
// owned by the test (see ownTestServer).
func testServer(t *testing.T) *Server {
	t.Helper()
	srv, setupDB := newOpenCodeTestServer(t)
	setupDB.Close()
	return srv
}

// --- Handler integration tests ---

func TestHandleStats_Empty(t *testing.T) {
	// Through the route table: the version is set after New, so this also
	// proves the analytics module reads it per request.
	srv := withReadyTestHistory(t, testServer(t)).WithVersion("v1.2.3-test")
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

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
	if stats["version"] != "v1.2.3-test" {
		t.Errorf("version = %v, want v1.2.3-test", stats["version"])
	}
}

func TestHandleProjects_Empty(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
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

	if err := srv.refreshProjectsIndex(t.Context()); err != nil {
		t.Fatalf("initial projects refresh: %v", err)
	}

	_, err = rawDB.Exec(
		`INSERT INTO session (id, title, directory, time_created, time_updated)
		 VALUES ('s-2', 'two', '/repo/two', 2000, 2000)`,
	)
	if err != nil {
		t.Fatalf("seeding second project session: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
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

	if err := srv.refreshProjectsIndex(t.Context()); err != nil {
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
	if err := srv.activity.Update(clientActivityLease{ClientID: "client", Visible: true, Scopes: []string{"projects"}, TTLMS: 45_000}); err != nil {
		t.Fatal(err)
	}
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
	srv.hostRouter = hostsvc.NewRouter(&ensureHost{Host: srv.router().Local(), ensure: func(_ context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureRuntimeResult, error) {
		return &hostsvc.EnsureRuntimeResult{Endpoint: "unix:///tmp/fake.sock", RepoRoot: req.ProjectDir}, nil
	}})

	if err := srv.refreshProjectsIndex(t.Context()); err != nil {
		t.Fatalf("initial projects refresh: %v", err)
	}
	sub, unsubscribe := srv.broadcastHub.subscribe()
	defer unsubscribe()

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
	select {
	case event := <-sub.ch:
		if event.event != "ocman.session.changed" {
			t.Fatalf("unexpected session creation event: %s", event.event)
		}
		var payload struct {
			SessionID string `json:"sessionID"`
			Session   struct {
				ID        string `json:"id"`
				Directory string `json:"directory"`
				Status    string `json:"status"`
			} `json:"session"`
		}
		if err := json.Unmarshal(event.data, &payload); err != nil {
			t.Fatalf("unmarshal event payload: %v (%s)", err, event.data)
		}
		if payload.SessionID != "new-session" {
			t.Fatalf("event sessionID = %q, want new-session", payload.SessionID)
		}
		if payload.Session.ID != "new-session" || payload.Session.Directory != "/repo/new" {
			t.Fatalf("event carried no usable provisional session row: %s", event.data)
		}
	case <-time.After(time.Second):
		t.Fatal("session creation did not notify frontend")
	}
	// The post-create refresh runs in a goroutine (off the request
	// path) so the heavy GetProjects aggregate doesn't block creation.
	// Poll briefly for the snapshot to reflect the new project.
	deadline := time.Now().Add(2 * time.Second)
	for {
		projects, _ := srv.projectsSnapshot()
		if len(projects) == 1 && projects[0].Directory == "/repo/new" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected projects index to include created project, got %+v", projects)
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case event := <-sub.ch:
		if event.event != "ocman.projects.changed" {
			t.Fatalf("unexpected projects refresh event: %s", event.event)
		}
	case <-time.After(time.Second):
		t.Fatal("projects refresh did not notify frontend")
	}
}

// TestHandleCreateSession_EnsuresBeforeCreate pins how session creation
// ensures the project's opencode instance before Create:
//   - remote: a remote session must ensure the instance on the owning host
//     (via the compound platform id) and pass the ensured port, fixing "no
//     running OpenCode instance for directory".
//   - local: an externally-killed local instance used to be only
//     *discovered* via lsof, failing forever; local Create must relaunch too.
//   - pinned to hub: a platform without a compound id is the hub's adapter,
//     so the ensure must never follow ForDir's directory inference to a
//     remote that happens to have the same absolute path.
//   - worktree: a worktree session folds the ensure back to the main
//     checkout's single shared instance, while the session itself still
//     lands in the worktree.
//   - failure: a local ensure is a soft prerequisite; a non-git directory
//     still creates against whatever instance discovery finds.
func TestHandleCreateSession_EnsuresBeforeCreate(t *testing.T) {
	for _, tc := range []struct {
		name, platform, dir string
		resolveToRemote     bool
		localErr            error
		wantLocal           string // directory ensured on the hub's host
		wantRemote          string // directory ensured on rem1
		wantPort            string
	}{
		{name: "remote", platform: "r-rem1:opencode", dir: "/home/dries", wantRemote: "/home/dries", wantPort: "7788"},
		{name: "local", platform: "opencode", dir: "/repo/killed", wantLocal: "/repo/killed", wantPort: "6611"},
		{name: "local pinned to hub", platform: "opencode", dir: "/repo/shared-path", resolveToRemote: true, wantLocal: "/repo/shared-path", wantPort: "6611"},
		{name: "worktree folds to project root", platform: "opencode", dir: "/src/.worktrees/ocman/feat-x", wantLocal: "/src/ocman", wantPort: "6611"},
		{name: "local failure falls back to discovery", platform: "opencode", dir: "/not/a/repo", localErr: errors.New("not a git repository"), wantLocal: "/not/a/repo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := testServer(t)
			var ensuredLocal, ensuredRemote string
			srv.hostRouter = hostsvc.NewRouter(&ensureHost{ensure: func(_ context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureRuntimeResult, error) {
				ensuredLocal = req.ProjectDir
				if tc.localErr != nil {
					return nil, tc.localErr
				}
				return &hostsvc.EnsureRuntimeResult{Endpoint: "http://127.0.0.1:6611", RepoRoot: req.ProjectDir}, nil
			}})
			srv.hostRouter.RegisterRemote("rem1", &ensureHost{owner: "rem1", ensure: func(_ context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureRuntimeResult, error) {
				ensuredRemote = req.ProjectDir
				return &hostsvc.EnsureRuntimeResult{Endpoint: "http://127.0.0.1:7788", RepoRoot: req.ProjectDir}, nil
			}})
			if tc.resolveToRemote {
				srv.hostRouter.SetDirResolver(func(string) string { return "rem1" })
			}

			var created *platforms.CreateSessionRequest
			reg := platforms.NewRegistry()
			reg.Register(&fakePlatform{id: tc.platform, createSessionFn: func(req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
				created = &req
				return &platforms.CreateSessionResponse{ID: "new-session"}, nil
			}})
			srv.registry = reg

			req := httptest.NewRequest(http.MethodPost, "/api/sessions",
				strings.NewReader(`{"platform":"`+tc.platform+`","directory":"`+tc.dir+`"}`))
			rr := httptest.NewRecorder()
			srv.handleCreateSession(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
			}
			if ensuredLocal != tc.wantLocal || ensuredRemote != tc.wantRemote {
				t.Fatalf("ensured local=%q remote=%q, want local=%q remote=%q", ensuredLocal, ensuredRemote, tc.wantLocal, tc.wantRemote)
			}
			if created == nil {
				t.Fatal("create was not attempted")
			}
			if created.Port != tc.wantPort || created.Directory != tc.dir {
				t.Fatalf("create got port=%q dir=%q, want port=%q dir=%q", created.Port, created.Directory, tc.wantPort, tc.dir)
			}
		})
	}
}

// TestSessionStateHandlers covers the seen, archive and pin handlers'
// request validation and their state effects. Rows share one server, so
// each uses its own session ID.
func TestSessionStateHandlers(t *testing.T) {
	srv := testServer(t)
	pinned := func(id string) bool {
		got, _ := srv.stateDB.PinnedSessions(t.Context())
		_, ok := got[state.Key{Platform: "opencode", SessionID: id}]
		return ok
	}
	session := func(w http.ResponseWriter, r *http.Request) { serveSessionRoute(srv.handleSession, w, r) }
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		path    string
		body    string // empty means GET
		seed    func() error
		want    int
		check   func() bool
	}{
		{name: "session not found", handler: session, path: "/api/session/nonexistent", want: http.StatusNotFound},
		{name: "session invalid id", handler: session, path: "/api/session/invalid!id", want: http.StatusBadRequest},
		{name: "seen invalid id", handler: srv.handleSeenSession, path: "/api/session/seen", body: `{"sessionId":"invalid!","timeUpdated":1000}`, want: http.StatusBadRequest},
		{name: "seen", handler: srv.handleSeenSession, path: "/api/session/seen", body: `{"platform":"opencode","sessionId":"seen1","timeUpdated":1000}`, want: http.StatusOK},
		{name: "archive", handler: srv.handleArchiveSession, path: "/api/session/archive", body: `{"platform":"opencode","sessionId":"arch1","timeUpdated":1000,"archived":true}`, want: http.StatusOK},
		{name: "unarchive", handler: srv.handleArchiveSession, path: "/api/session/archive", body: `{"platform":"opencode","sessionId":"arch2","archived":false}`, want: http.StatusOK},
		{name: "archive missing timeUpdated", handler: srv.handleArchiveSession, path: "/api/session/archive", body: `{"sessionId":"arch3","archived":true}`, want: http.StatusBadRequest},
		{name: "archive invalid JSON", handler: srv.handleArchiveSession, path: "/api/session/archive", body: `{invalid}`, want: http.StatusBadRequest},
		{name: "pin", handler: srv.handlePinSession, path: "/api/session/pin", body: `{"platform":"opencode","sessionId":"pin1","pinned":true}`, want: http.StatusOK, check: func() bool { return pinned("pin1") }},
		{
			name: "unpin", handler: srv.handlePinSession, path: "/api/session/pin", body: `{"platform":"opencode","sessionId":"pin2","pinned":false}`, want: http.StatusOK,
			seed:  func() error { return srv.stateDB.PinSession(t.Context(), "opencode", "pin2") },
			check: func() bool { return !pinned("pin2") },
		},
		{name: "pin invalid id", handler: srv.handlePinSession, path: "/api/session/pin", body: `{"platform":"opencode","sessionId":"invalid!","pinned":true}`, want: http.StatusBadRequest},
		{name: "pin invalid JSON", handler: srv.handlePinSession, path: "/api/session/pin", body: `{invalid}`, want: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.seed != nil {
				if err := tc.seed(); err != nil {
					t.Fatal(err)
				}
			}
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if tc.body != "" {
				req = httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			}
			rr := httptest.NewRecorder()
			tc.handler(rr, req)
			if rr.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rr.Code, tc.want, rr.Body.String())
			}
			if tc.check != nil && !tc.check() {
				t.Fatal("state was not updated")
			}
		})
	}
}

// TestHandleArchiveSession_StaleClientTimestamp reproduces the
// "archived session reappears in the sidebar" bug: the client sends
// the timeUpdated from its cached sidebar row, which can lag the
// session's real time_updated (the server-side sessions cache has a
// 15 s TTL, and a busy session keeps advancing). Storing that stale
// value verbatim made the very next applySessionState pass see
// TimeUpdated > archivedAtUpdate and silently delete the archive
// record. The handler must clamp the stored timestamp to "now" so
// only activity strictly after the archive click resurfaces the
// session.
func TestHandleArchiveSession_StaleClientTimestamp(t *testing.T) {
	srv := testServer(t)

	// Client archives with a stale cached timestamp (1000) while the
	// session's real time_updated is already 2000.
	body := strings.NewReader(`{"platform":"opencode","sessionId":"s1","timeUpdated":1000,"archived":true}`)
	req := httptest.NewRequest(http.MethodPost, "/api/session/archive", body)
	rr := httptest.NewRecorder()
	srv.handleArchiveSession(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	sessions := []db.Session{
		{ID: "s1", Platform: "opencode", TimeUpdated: 2000},
	}
	if err := srv.applySessionState(t.Context(), sessions); err != nil {
		t.Fatalf("applySessionState: %v", err)
	}
	if !sessions[0].Archived {
		t.Error("s1 must stay archived despite a stale client timeUpdated")
	}
	archived, _ := srv.stateDB.ArchivedSessions(t.Context())
	if _, ok := archived[state.Key{Platform: "opencode", SessionID: "s1"}]; !ok {
		t.Error("archive record must not be deleted by the next sessions poll")
	}
}

// TestHandleArchiveSession_RemoteClockSkew reproduces the "archiving
// remote sessions doesn't stick" bug. A remote session's TimeUpdated
// originates on the remote's clock, which can run ahead of the hub's.
// The old handler clamped the stored archived_at to the hub's now();
// on the next sessions poll / reconnect applySessionState then saw the
// remote-clock TimeUpdated > hub-clock archived_at and auto-unarchived
// the session. For remote (compound r-...:) platforms the client value
// must be stored verbatim so the comparison stays on one clock.
func TestHandleArchiveSession_RemoteClockSkew(t *testing.T) {
	srv := testServer(t)
	const remotePlatform = "r-abc:opencode"
	srv.registry.Register(&fakePlatform{id: remotePlatform})

	// The remote's clock runs far ahead of the hub: its session's
	// time_updated is 5,000,000,000,000 (year ~2128) while the hub's
	// now() is ~2025. The client archives with that remote-clock value.
	//
	// The old handler clamped the stored baseline: archived_at =
	// max(remoteTime, hubNow) = remoteTime here, which was fine — but if
	// the remote clock were *behind* the hub, the clamp bumped the
	// baseline up to hubNow, and the next poll (remote-clock TimeUpdated
	// < hubNow) is a different-clock comparison. The general rule: a
	// remote session's baseline must be stored on the *remote* clock
	// (the client value), never clamped to the hub clock.
	const remoteTime = int64(5_000_000_000_000)
	body := strings.NewReader(`{"platform":"r-abc:opencode","sessionId":"s1","timeUpdated":5000000000000,"archived":true}`)
	req := httptest.NewRequest(http.MethodPost, "/api/session/archive", body)
	rr := httptest.NewRecorder()
	srv.handleArchiveSession(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Baseline must be the remote-clock value, verbatim.
	archived, _ := srv.stateDB.ArchivedSessions(t.Context())
	got := archived[state.Key{Platform: remotePlatform, SessionID: "s1"}]
	if got != remoteTime {
		t.Fatalf("stored archived_at = %d, want remote clock %d", got, remoteTime)
	}

	// Next poll re-stamps s1 at the same remote time (no new activity):
	// it must stay archived.
	sessions := []db.Session{
		{ID: "s1", Platform: remotePlatform, TimeUpdated: remoteTime},
	}
	if err := srv.applySessionState(t.Context(), sessions); err != nil {
		t.Fatalf("applySessionState: %v", err)
	}
	if !sessions[0].Archived {
		t.Error("remote s1 must stay archived across reconnects (clock skew)")
	}

	// Now the remote clock is *behind* the hub: archive a session whose
	// remote time_updated (2000) is far below the hub's now(). The old
	// clamp stored hubNow (~1.7e12); the next poll, seeing the
	// remote-clock TimeUpdated (2000) < hubNow, would treat it as
	// "archived and untouched" only by luck — but any later remote
	// activity below hubNow could never resurface it, and a remote
	// slightly ahead of hub resurfaces immediately. Verbatim storage
	// keeps the comparison on one clock.
	srv.registry.Register(&fakePlatform{id: "r-def:opencode"})
	body2 := strings.NewReader(`{"platform":"r-def:opencode","sessionId":"s2","timeUpdated":2000,"archived":true}`)
	req2 := httptest.NewRequest(http.MethodPost, "/api/session/archive", body2)
	rr2 := httptest.NewRecorder()
	srv.handleArchiveSession(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr2.Code, rr2.Body.String())
	}
	archived2, _ := srv.stateDB.ArchivedSessions(t.Context())
	if got := archived2[state.Key{Platform: "r-def:opencode", SessionID: "s2"}]; got != 2000 {
		t.Fatalf("stored archived_at = %d, want remote clock 2000 (not hub-clamped)", got)
	}
}

// Note: the old /api/agents and /api/commands tests (and the
// primePortCache helper) have moved with their implementations into the
// internal/platforms/opencode/provider package. The server layer now dispatches
// every session-scoped operation through the Platform adapter, so the
// proxy behaviour is covered by the adapter's own tests rather than the
// server's.

// --- applySessionState tests ---

// TestApplySessionState_Overlays checks the seen, archive and pin overlays
// applySessionState puts on each session.
func TestApplySessionState_Overlays(t *testing.T) {
	type overlay struct {
		Seen, Archived, Pinned bool
		SeenTimeUpdated        int64
	}
	// Later than any archive timestamp, whatever its clock.
	const afterArchive = int64(1) << 53
	for _, tc := range []struct {
		name     string
		seed     func(context.Context, *state.DB) error
		sessions []db.Session
		want     []overlay
		check    func(*testing.T, *Server)
	}{
		{
			name:     "marks seen",
			seed:     func(ctx context.Context, st *state.DB) error { return st.MarkSessionSeen(ctx, "opencode", "s1", 2000) },
			sessions: []db.Session{{ID: "s1", Platform: "opencode", TimeUpdated: 2000}, {ID: "s2", Platform: "opencode", TimeUpdated: 3000}},
			want:     []overlay{{Seen: true, SeenTimeUpdated: 2000}, {}},
		},
		{
			// The last-seen cutoff is surfaced even after newer updates, so
			// the frontend can render a "jump to first unread" marker.
			name:     "seen cutoff survives newer updates",
			seed:     func(ctx context.Context, st *state.DB) error { return st.MarkSessionSeen(ctx, "opencode", "s1", 1500) },
			sessions: []db.Session{{ID: "s1", Platform: "opencode", TimeUpdated: 2500}, {ID: "s2", Platform: "opencode", TimeUpdated: 3000}},
			want:     []overlay{{SeenTimeUpdated: 1500}, {}},
		},
		{
			name:     "marks archived",
			seed:     func(ctx context.Context, st *state.DB) error { return st.ArchiveSession(ctx, "opencode", "s1", 2000) },
			sessions: []db.Session{{ID: "s1", Platform: "opencode", TimeUpdated: 2000}},
			want:     []overlay{{Archived: true}},
		},
		{
			name:     "auto-unarchives an updated session",
			seed:     func(ctx context.Context, st *state.DB) error { return st.ArchiveSession(ctx, "opencode", "s1", 1000) },
			sessions: []db.Session{{ID: "s1", Platform: "opencode", TimeUpdated: 2000}},
			want:     []overlay{{}},
			check: func(t *testing.T, srv *Server) {
				archived, _ := srv.stateDB.ArchivedSessions(t.Context())
				if _, ok := archived[state.Key{Platform: "opencode", SessionID: "s1"}]; ok {
					t.Error("s1 should no longer be in archived_session table")
				}
			},
		},
		{
			// Regression: an archived project used to leave its sessions
			// "active", so the root "/" redirect could jump into a session
			// the sidebar (which hides archived projects) never shows.
			name: "marks archived by project",
			seed: func(ctx context.Context, st *state.DB) error {
				return st.ArchiveProject(ctx, state.LocalRemoteID, "/src/foo")
			},
			sessions: []db.Session{
				{ID: "s1", Platform: "opencode", Directory: "/src/foo", TimeUpdated: 1000},
				{ID: "s2", Platform: "opencode", Directory: "/src/bar", TimeUpdated: 1000},
			},
			want: []overlay{{Archived: true}, {}},
		},
		{
			// Matches the project auto-unarchive rule: handleProjects drops
			// the project marker once it sees the newer activity.
			name: "project archive respects newer activity",
			seed: func(ctx context.Context, st *state.DB) error {
				return st.ArchiveProject(ctx, state.LocalRemoteID, "/src/foo")
			},
			sessions: []db.Session{{ID: "s1", Platform: "opencode", Directory: "/src/foo", TimeUpdated: afterArchive}},
			want:     []overlay{{}},
		},
		{
			// The multi-platform point of the state.db migration (AD-10).
			name: "archive scopes by platform",
			seed: func(ctx context.Context, st *state.DB) error {
				return st.ArchiveSession(ctx, "opencode", "shared-id", 1000)
			},
			sessions: []db.Session{{ID: "shared-id", Platform: "opencode", TimeUpdated: 1000}, {ID: "shared-id", Platform: "other-platform", TimeUpdated: 1000}},
			want:     []overlay{{Archived: true}, {}},
		},
		{
			name:     "marks pinned",
			seed:     func(ctx context.Context, st *state.DB) error { return st.PinSession(ctx, "opencode", "s1") },
			sessions: []db.Session{{ID: "s1", Platform: "opencode", TimeUpdated: 2000}, {ID: "s2", Platform: "opencode", TimeUpdated: 3000}},
			want:     []overlay{{Pinned: true}, {}},
		},
		{
			name:     "pin scopes by platform",
			seed:     func(ctx context.Context, st *state.DB) error { return st.PinSession(ctx, "opencode", "shared-id") },
			sessions: []db.Session{{ID: "shared-id", Platform: "opencode", TimeUpdated: 1000}, {ID: "shared-id", Platform: "other-platform", TimeUpdated: 1000}},
			want:     []overlay{{Pinned: true}, {}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := testServer(t)
			if err := tc.seed(t.Context(), srv.stateDB); err != nil {
				t.Fatal(err)
			}
			if err := srv.applySessionState(t.Context(), tc.sessions); err != nil {
				t.Fatalf("applySessionState: %v", err)
			}
			for i, s := range tc.sessions {
				got := overlay{Seen: s.Seen, Archived: s.Archived, Pinned: s.Pinned, SeenTimeUpdated: s.SeenTimeUpdated}
				if got != tc.want[i] {
					t.Errorf("%s/%s overlay = %+v, want %+v", s.Platform, s.ID, got, tc.want[i])
				}
				if s.Pinned != (s.PinnedAt > 0) {
					t.Errorf("%s/%s Pinned = %v with PinnedAt %d", s.Platform, s.ID, s.Pinned, s.PinnedAt)
				}
			}
			if tc.check != nil {
				tc.check(t, srv)
			}
		})
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
	mustExec(`INSERT INTO message (id, session_id, time_created, data) VALUES ('m1', 's1', 100, '{"role":"user"}')`)
	mustExec(`INSERT INTO message (id, session_id, time_created, data) VALUES ('m2', 's1', 250, '{"role":"assistant","finish":"stop"}')`)
	mustExec(`INSERT INTO message (id, session_id, time_created, data) VALUES ('m3', 's1', 400, '{"role":"assistant","finish":"stop"}')`)
	mustExec(`INSERT INTO message (id, session_id, time_created, data) VALUES ('m4', 's2', 150, '{"role":"user"}')`)
	mustExec(`INSERT INTO message (id, session_id, time_created, data) VALUES ('m5', 's2', 300, '{"role":"assistant","finish":"stop"}')`)
	ingestRaw(t, srv, 2)
	s1, s2 := canonicalID(t, srv, "s1"), canonicalID(t, srv, "s2")

	// s1: user saw up to time_updated=200 → 2 messages newer (m2, m3).
	// s2: user saw up to time_updated=300 → fully seen, count omitted.
	if err := srv.stateDB.MarkSessionSeen(t.Context(), "opencode", s1, 200); err != nil {
		t.Fatal(err)
	}
	if err := srv.stateDB.MarkSessionSeen(t.Context(), "opencode", s2, 300); err != nil {
		t.Fatal(err)
	}

	sessions := []db.Session{
		{ID: s1, Platform: "opencode", TimeUpdated: 400},
		{ID: s2, Platform: "opencode", TimeUpdated: 300},
	}
	if err := srv.applySessionState(t.Context(), sessions); err != nil {
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
	mustExec(`INSERT INTO message (id, session_id, time_created, data) VALUES ('m1', 's1', 100, '{"role":"user"}')`)
	mustExec(`INSERT INTO message (id, session_id, time_created, data) VALUES ('m2', 's1', 200, '{"role":"assistant","finish":"stop"}')`)
	mustExec(`INSERT INTO message (id, session_id, time_created, data) VALUES ('m3', 's1', 300, '{"role":"user"}')`)
	ingestRaw(t, srv, 1)

	sessions := []db.Session{
		{ID: canonicalID(t, srv, "s1"), Platform: "opencode", TimeUpdated: 500},
	}
	if err := srv.applySessionState(t.Context(), sessions); err != nil {
		t.Fatalf("applySessionState: %v", err)
	}
	if sessions[0].UnreadCount != 3 {
		t.Errorf("never-seen UnreadCount: want 3, got %d", sessions[0].UnreadCount)
	}
	if sessions[0].SeenTimeUpdated != 0 {
		t.Errorf("never-seen SeenTimeUpdated: want 0, got %d", sessions[0].SeenTimeUpdated)
	}
}

// --- graceful shutdown test ---

func TestServerStart_ShutdownOnCancel(t *testing.T) {
	t.Setenv("OCMAN_PLUGIN_DIR", t.TempDir())
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

func TestServerStart_WiresRoutinesAndRetiresOldSurfaces(t *testing.T) {
	pluginDir := t.TempDir()
	t.Setenv("OCMAN_PLUGIN_DIR", pluginDir)
	writeDiscoveryPlugin(t, pluginDir, "ocman-plugin-startup", "org.example.startup", "1")
	srv := testServer(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.StartOnListener(ctx, ln) }()

	baseURL := "http://" + ln.Addr().String()
	var resp *http.Response
	for {
		resp, err = http.Get(baseURL + "/api/routines")
		if err == nil {
			break
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-t.Context().Done():
			t.Fatal(err)
		}
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("routine list status = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()

	plugin, err := srv.stateDB.GetPlugin(ctx, "org.example.startup")
	if err != nil || plugin.Enabled || plugin.Removed {
		t.Fatalf("startup discovery: %+v, %v", plugin, err)
	}

	resp, err = http.Post(baseURL+"/api/routines", "application/json", strings.NewReader(validRoutineBody))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		resp.Body.Close()
		t.Fatalf("routine create status = %d, want 201", resp.StatusCode)
	}
	resp.Body.Close()

	for _, path := range []string{
		"/api/prompt-schedules",
		"/api/dagu/status",
		"/api/workflows",
		"/api/workflow-runs",
		"/api/workflow-steps",
	} {
		resp, err = http.Get(baseURL + path)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusNotFound {
			resp.Body.Close()
			t.Fatalf("retired route %s status = %d, want 404", path, resp.StatusCode)
		}
		resp.Body.Close()
	}
	for _, path := range []string{"/", "/robots.txt", "/routines"} {
		resp, err = http.Get(baseURL + path)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			t.Fatalf("web route %s status = %d, want 200", path, resp.StatusCode)
		}
		resp.Body.Close()
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestServerRetainedStartupWiring(t *testing.T) {
	srv := testServer(t)
	if srv.SessionService() == nil || srv.Registry() == nil || len((registryRef{srv}).Platforms()) != 1 {
		t.Fatal("session platform wiring is incomplete")
	}
	if srv.WithAutoApproveDefault(true) != srv || !srv.autoApproveDefault {
		t.Fatal("auto-approve startup option was not applied")
	}
	if srv.WithMCPAddr("127.0.0.1:0") != srv || srv.mcpAddr != "127.0.0.1:0" {
		t.Fatal("MCP startup option was not applied")
	}
	if srv.RemoteServerHost() != srv.HostRouter().Local() {
		t.Fatal("remote server and HTTP server use different local hosts")
	}

	store := managedStoreOrNil(srv.stateDB)
	instance := hostlocal.ManagedInstance{Endpoint: "http://127.0.0.1:4321", Kind: "native", RuntimeID: "runtime", PID: 42, LaunchedAt: time.Unix(100, 0)}
	if err := store.Upsert(t.Context(), "/repo", instance); err != nil {
		t.Fatal(err)
	}
	instances, err := store.List(t.Context())
	got := instances["/repo"]
	if err != nil || got.Endpoint != instance.Endpoint || got.Kind != instance.Kind || got.RuntimeID != instance.RuntimeID || got.PID != instance.PID || !got.LaunchedAt.Equal(instance.LaunchedAt) {
		t.Fatalf("managed instances = %+v, %v", instances, err)
	}
	if err := store.Delete(t.Context(), "/repo"); err != nil {
		t.Fatal(err)
	}
	if instances, err = store.List(t.Context()); err != nil || len(instances) != 0 {
		t.Fatalf("managed instances after delete = %+v, %v", instances, err)
	}
	if managedStoreOrNil(nil) != nil {
		t.Fatal("nil state database produced a managed store")
	}
	closedState := openTestStateDB(t)
	_ = closedState.Close()
	if _, err := managedStoreOrNil(closedState).List(t.Context()); err == nil {
		t.Fatal("managed store listing hid a database failure")
	}

	local := &projectHandleRemoteHost{remoteID: "local", upstreams: &hostsvc.ProjectUpstreams{RepoRoot: "/repo"}}
	resolverServer := &Server{hostRouter: hostsvc.NewRouter(local)}
	root, err := (factoryProjectResolver{resolverServer}).ResolveLocalProject(t.Context(), "/repo/subdir")
	if err != nil || root != "/repo" {
		t.Fatalf("resolved root = %q, %v", root, err)
	}
	local.upstreamErr = errors.New("upstreams unavailable")
	if _, err := (factoryProjectResolver{resolverServer}).ResolveLocalProject(t.Context(), "/repo"); err == nil {
		t.Fatal("project resolution ignored the host error")
	}
	remote := &projectHandleRemoteHost{remoteID: "remote"}
	if _, err := (factoryProjectResolver{&Server{hostRouter: hostsvc.NewRouter(remote)}}).ResolveLocalProject(t.Context(), "/repo"); err == nil {
		t.Fatal("Factory accepted a remote project")
	}
}

// TestAutoArchive_UsesRegistry verifies the auto-archive pass iterates
// registered agents (as opposed to calling s.db directly) so that every
// platform's sessions are picked up for the same archive logic.
func TestAutoArchive_UsesRegistry(t *testing.T) {
	srv, rawDB := testServerWithRawDB(t)
	defer rawDB.Close()

	eightDaysAgo := time.Now().Add(-8 * 24 * time.Hour).UnixMilli()
	fourDaysAgo := time.Now().Add(-4 * 24 * time.Hour).UnixMilli()
	_, err := rawDB.Exec(
		`INSERT INTO session (id, title, directory, time_created, time_updated) VALUES
		 ('old-session', 'ancient', '/tmp', ?, ?),
		 ('recent-session', 'recent', '/tmp', ?, ?)`,
		eightDaysAgo, eightDaysAgo, fourDaysAgo, fourDaysAgo,
	)
	if err != nil {
		t.Fatalf("seeding session: %v", err)
	}
	ingestRaw(t, srv, 2)

	srv.autoArchiveInactiveSessions(t.Context())

	archived, err := srv.stateDB.ArchivedSessions(t.Context())
	if err != nil {
		t.Fatalf("ArchivedSessions: %v", err)
	}
	if _, ok := archived[state.Key{Platform: "opencode", SessionID: canonicalID(t, srv, "old-session")}]; !ok {
		t.Errorf("expected old-session to be archived under opencode after auto-archive pass, got %+v", archived)
	}
	if _, ok := archived[state.Key{Platform: "opencode", SessionID: canonicalID(t, srv, "recent-session")}]; ok {
		t.Errorf("expected recent-session to remain unarchived, got %+v", archived)
	}
}

func TestAutoArchiveUsesConfiguredTTL(t *testing.T) {
	srv, rawDB := testServerWithRawDB(t)
	defer rawDB.Close()

	fourDaysAgo := time.Now().Add(-4 * 24 * time.Hour).UnixMilli()
	if _, err := rawDB.Exec(
		`INSERT INTO session (id, title, directory, time_created, time_updated)
		 VALUES ('custom-ttl-session', 'old enough', '/tmp', ?, ?)`,
		fourDaysAgo, fourDaysAgo,
	); err != nil {
		t.Fatalf("seeding session: %v", err)
	}
	ingestRaw(t, srv, 1)
	if err := srv.setAutoArchiveSettings(t.Context(), autoArchiveSettingsView{Enabled: true, TTLDays: 3}); err != nil {
		t.Fatal(err)
	}

	srv.autoArchiveInactiveSessions(t.Context())

	archived, err := srv.stateDB.ArchivedSessions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := archived[state.Key{Platform: "opencode", SessionID: canonicalID(t, srv, "custom-ttl-session")}]; !ok {
		t.Errorf("session older than configured TTL was not archived: %+v", archived)
	}
}

func TestAutoArchiveCanBeDisabled(t *testing.T) {
	srv, rawDB := testServerWithRawDB(t)
	defer rawDB.Close()

	if _, err := rawDB.Exec(
		`INSERT INTO session (id, title, directory, time_created, time_updated)
		 VALUES ('disabled-session', 'ancient', '/tmp/disabled', 1000, 1000)`,
	); err != nil {
		t.Fatalf("seeding session: %v", err)
	}
	if err := srv.setAutoArchiveSettings(t.Context(), autoArchiveSettingsView{Enabled: false, TTLDays: 7}); err != nil {
		t.Fatal(err)
	}
	opencodeplatform.ResetCaches()

	srv.autoArchiveInactiveSessions(t.Context())
	srv.autoArchiveInactiveProjects(t.Context())

	archivedSessions, err := srv.stateDB.ArchivedSessions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := archivedSessions[state.Key{Platform: "opencode", SessionID: "disabled-session"}]; ok {
		t.Error("session was archived while auto-archive was disabled")
	}
	archivedProjects, err := srv.stateDB.ArchivedProjects(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := archivedProjects[localProjectKey("/tmp/disabled")]; ok {
		t.Error("project was archived while auto-archive was disabled")
	}
}

// TestAutoArchiveProjects applies the configured TTL to project activity.
func TestAutoArchiveProjects(t *testing.T) {
	srv, rawDB := testServerWithRawDB(t)
	defer rawDB.Close()

	fourDaysAgo := time.Now().Add(-4 * 24 * time.Hour).UnixMilli()
	twoDaysAgo := time.Now().Add(-2 * 24 * time.Hour).UnixMilli()
	_, err := rawDB.Exec(
		`INSERT INTO session (id, title, directory, time_created, time_updated) VALUES
		 ('old-session', 'ancient', '/tmp/stale', ?, ?),
		 ('new-session', 'recent', '/tmp/active', ?, ?)`,
		fourDaysAgo, fourDaysAgo, twoDaysAgo, twoDaysAgo,
	)
	if err != nil {
		t.Fatalf("seeding sessions: %v", err)
	}
	if err := srv.setAutoArchiveSettings(t.Context(), autoArchiveSettingsView{Enabled: true, TTLDays: 3}); err != nil {
		t.Fatal(err)
	}
	opencodeplatform.ResetCaches()

	srv.autoArchiveInactiveProjects(t.Context())

	archived, err := srv.stateDB.ArchivedProjects(t.Context())
	if err != nil {
		t.Fatalf("ArchivedProjects: %v", err)
	}
	if _, ok := archived[localProjectKey("/tmp/stale")]; !ok {
		t.Errorf("expected /tmp/stale to be auto-archived, got %+v", archived)
	}
	if _, ok := archived[localProjectKey("/tmp/active")]; ok {
		t.Errorf("expected /tmp/active to stay unarchived, got %+v", archived)
	}
}

// TestAutoArchiveRespectsRecentUnarchive pins that unarchiving is not
// silently undone. Unarchive only DELETEs the marker, and the archive
// loops re-derive state purely from inactivity — and run immediately at
// boot — so unarchiving an old session or project to look at it and
// then restarting ocman re-hid it.
func TestAutoArchiveRespectsRecentUnarchive(t *testing.T) {
	srv, rawDB := testServerWithRawDB(t)
	defer rawDB.Close()

	if _, err := rawDB.Exec(
		`INSERT INTO session (id, title, directory, time_created, time_updated)
		 VALUES ('old-session', 'ancient', '/tmp/stale', 1000, 1000)`,
	); err != nil {
		t.Fatalf("seeding session: %v", err)
	}
	opencodeplatform.ResetCaches()

	// The user goes looking for the old work and unarchives it.
	if err := srv.stateDB.UnarchiveSession(t.Context(), "opencode", "old-session"); err != nil {
		t.Fatal(err)
	}
	if err := srv.stateDB.UnarchiveProject(t.Context(), state.LocalRemoteID, projectRootForDirectory("/tmp/stale")); err != nil {
		t.Fatal(err)
	}

	// ocman restarts for an unrelated reason; both loops run at boot.
	srv.autoArchiveInactiveSessions(t.Context())
	srv.autoArchiveInactiveProjects(t.Context())

	archivedSessions, err := srv.stateDB.ArchivedSessions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := archivedSessions[state.Key{Platform: "opencode", SessionID: "old-session"}]; ok {
		t.Error("session the user just unarchived was silently re-archived")
	}
	archivedProjects, err := srv.stateDB.ArchivedProjects(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := archivedProjects[localProjectKey("/tmp/stale")]; ok {
		t.Error("project the user just unarchived was silently re-archived")
	}
}

// testServerWithRawDB is like testServer but also returns a raw *sql.DB
// handle to the same OpenCode database so tests can seed additional rows.
// It also resets the process-wide OpenCode caches.
func testServerWithRawDB(t *testing.T) (*Server, *sql.DB) {
	t.Helper()
	// Process-wide OpenCode HTTP caches are keyed by port; start clean.
	opencodeplatform.ResetCaches()
	return newOpenCodeTestServer(t)
}

// newOpenCodeTestServer builds the shared OpenCode test server, owned by the
// test (see ownTestServer), and returns the writable connection used to
// create the schema; the caller owns it.
func newOpenCodeTestServer(t *testing.T) (*Server, *sql.DB) {
	t.Helper()
	tmpDir := t.TempDir()

	tmpOC := tmpDir + "/opencode.db"
	setupDB, err := sql.Open("sqlite", tmpOC)
	if err != nil {
		t.Fatalf("opening temp oc file: %v", err)
	}
	_, err = setupDB.Exec(`
		CREATE TABLE project (id TEXT PRIMARY KEY, worktree TEXT NOT NULL DEFAULT '');
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

	// Open in read-write mode for tests (the production code uses read-only,
	// but tests need to work with a freshly created empty database).
	database, err := db.OpenReadWrite(tmpOC)
	if err != nil {
		setupDB.Close()
		t.Fatalf("opening db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	stateDB, err := state.Open(testStateAt(t, tmpDir+"/state.db"), ModuleSchemas()...)
	if err != nil {
		setupDB.Close()
		t.Fatalf("opening state db: %v", err)
	}
	t.Cleanup(func() { stateDB.Close() })

	reg, history := canonicalOpenCodeRegistry(t, database, stateDB)
	srv := New(database, stateDB, "127.0.0.1:0", reg, nil)
	testHistories.Store(srv, history)
	installProjectCacheFixture(t, srv, database)
	// Never spawn real tmux/opencode from tests — that leaks a tmux
	// session per run (the temp dir is cleaned up, the session isn't).
	// Override before the host router is built lazily on first use.
	srv.runtime = fakeRuntime{}
	ownTestServer(t, srv)
	return srv, setupDB
}

var testHistories sync.Map // *Server -> *statev2.DB for the OpenCode test servers

// ingestRaw wires history ingestion into an OpenCode test server, as startup
// does, and waits for want seeded sessions.
func ingestRaw(t *testing.T, s *Server, want int) {
	t.Helper()
	history, ok := testHistories.Load(s)
	if !ok {
		t.Fatal("server was not built by testServer or testServerWithRawDB")
	}
	if s.historyIngest == nil {
		s.WithConversationHistory(history.(*statev2.DB), "owner")
	}
	ingestHistory(t, s, history.(*statev2.DB), want)
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
	ingestRaw(t, srv, 2)
	mid, old := canonicalID(t, srv, "oc-mid"), canonicalID(t, srv, "oc-old")

	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
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
	wantIDs := []string{"cc-newest", mid, old, "cc-oldest"}
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
	ingestRaw(t, srv, 1)
	id := canonicalID(t, srv, "s1")

	req := httptest.NewRequest(http.MethodGet, "/api/session/"+id+"/changes", nil)
	req.URL.Path = "/api/session/" + id + "/changes"
	req.SetPathValue("id", id)
	rr := httptest.NewRecorder()
	serveSessionRoute(srv.handleSessionChanges, rr, req)

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
// adapters that report ErrUnsupported yield an HTTP 200
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

	req := httptest.NewRequest(http.MethodGet, "/api/session/fk-1/changes", nil)
	req.URL.Path = "/api/session/fk-1/changes"
	rr := httptest.NewRecorder()
	serveSessionRoute(srv.handleSessionChanges, rr, req)

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

	req := httptest.NewRequest(http.MethodGet, "/api/session/fk-1/info", nil)
	req.URL.Path = "/api/session/fk-1/info"
	rr := httptest.NewRecorder()
	serveSessionRoute(srv.handleSessionInfo, rr, req)

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

func TestHandleSessionInfo_EnrichesPersistedCommits(t *testing.T) {
	srv, rawDB := testServerWithRawDB(t)
	defer rawDB.Close()
	fakeSessions := []db.Session{{ID: "fk-commit", Platform: "fake", Directory: "/x", TimeUpdated: 1}}
	srv.registry.Register(&fakePlatform{id: "fake", sessions: fakeSessions, info: &platforms.SessionInfo{SessionID: "fk-commit"}})
	srv.registry.RememberSessions("fake", fakeSessions)
	branch := "old-name"
	if _, err := srv.stateDB.RecordSessionCommit(t.Context(), state.SessionCommit{
		Platform: "fake", SessionID: "fk-commit", SHA: "abc1234", Branch: &branch,
		Subject: "kept", SourceMessageID: "m1", ToolPartID: "p1", ToolCallID: "c1", SourceCallID: "c1",
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/session/fk-commit/info", nil)
	req.URL.Path = "/api/session/fk-commit/info"
	rr := httptest.NewRecorder()
	serveSessionRoute(srv.handleSessionInfo, rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var got platforms.SessionInfo
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Commits) != 1 || got.Commits[0].SHA != "abc1234" || got.Commits[0].Branch == nil || *got.Commits[0].Branch != "old-name" {
		t.Fatalf("commits = %#v", got.Commits)
	}
	if !got.CommitCaptureSupported {
		t.Fatal("commit capture should be reported as supported")
	}
}

func TestHandleSessionInfo_DoesNotEnrichRemoteFromHubState(t *testing.T) {
	srv, rawDB := testServerWithRawDB(t)
	defer rawDB.Close()
	const platformID = "r-owner:opencode"
	const sessionID = "same-session"
	srv.registry.Register(&fakePlatform{
		id:       platformID,
		sessions: []db.Session{{ID: sessionID, Platform: platformID}},
		info: &platforms.SessionInfo{
			SessionID: sessionID, CommitCaptureSupported: true,
			Commits: []platforms.SessionCommit{{Order: 1, SHA: "owner123", Subject: "owner"}},
		},
	})
	if _, err := srv.stateDB.RecordSessionCommit(t.Context(), state.SessionCommit{
		Platform: platformID, SessionID: sessionID, SHA: "hub4567", Subject: "hub",
		SourceMessageID: "same-message", ToolPartID: "same-part", ToolCallID: "same-call", SourceCallID: "same-call",
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/session/"+sessionID+"/info?platform="+platformID, nil)
	rr := httptest.NewRecorder()
	serveSessionRoute(srv.handleSessionInfo, rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var got platforms.SessionInfo
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Commits) != 1 || got.Commits[0].SHA != "owner123" {
		t.Fatalf("remote commits = %#v", got.Commits)
	}
}

// TestHandleSessionInfo_UnsupportedReturns200 verifies that adapters
// reporting ErrUnsupported (e.g. OpenCode without a live port) yield
// an HTTP 200 with Supported=false and non-nil empty
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

	req := httptest.NewRequest(http.MethodGet, "/api/session/fk-1/info", nil)
	req.URL.Path = "/api/session/fk-1/info"
	rr := httptest.NewRecorder()
	serveSessionRoute(srv.handleSessionInfo, rr, req)

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

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/notify", nil)
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

// localProjectKey is the archive identity of a project on the machine ocman
// runs on: (local, folded root).
func localProjectKey(directory string) state.ProjectKey {
	return state.ProjectKey{RemoteID: state.LocalRemoteID, Root: projectRootForDirectory(directory)}
}
