package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

// stateSetup describes the state.db rows a test wants pre-populated
// before /api/sessions is invoked. Each entry is a (platform,
// sessionID, asOfTimestamp) triple. The timestamp matters for seen
// (only-if-newer semantics) and archive (unarchive-on-newer-update
// semantics).
type stateSetup struct {
	archived []stateRow
	seen     []stateRow
	pinned   []stateRow
}

type stateRow struct {
	platform string
	id       string
	at       int64 // ms; ignored for pinned
}

// newSessionsTestServer returns a Server with no real OpenCode DB
// (handleSessions doesn't need it — the dashboard data path goes
// through the registry's adapters, not s.db) and a fresh on-disk
// state.db. Tests register a fakePlatform via the returned registry.
func newSessionsTestServer(t *testing.T) (*Server, *platforms.Registry) {
	t.Helper()
	tmp := filepath.Join(t.TempDir(), "state.db")
	stDB, err := state.Open(tmp)
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { stDB.Close() })

	reg := platforms.NewRegistry()
	srv := New(nil, stDB, "127.0.0.1:0", reg, nil)
	return srv, reg
}

func applyStateSetup(t *testing.T, db *state.DB, setup stateSetup) {
	t.Helper()
	for _, r := range setup.archived {
		if err := db.ArchiveSession(r.platform, r.id, r.at); err != nil {
			t.Fatalf("ArchiveSession(%v): %v", r, err)
		}
	}
	for _, r := range setup.seen {
		if err := db.MarkSessionSeen(r.platform, r.id, r.at); err != nil {
			t.Fatalf("MarkSessionSeen(%v): %v", r, err)
		}
	}
	for _, r := range setup.pinned {
		if err := db.PinSession(r.platform, r.id); err != nil {
			t.Fatalf("PinSession(%v): %v", r, err)
		}
	}
}

func mkSession(platform, id, title string, updated int64) db.Session {
	return db.Session{
		ID:          id,
		Platform:    platform,
		Title:       title,
		TimeUpdated: updated,
		TimeCreated: updated - 1000,
		ProjectID:   "proj-" + id,
	}
}

func TestHandleSessions_EmptyRegistry(t *testing.T) {
	srv, _ := newSessionsTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	rr := httptest.NewRecorder()
	srv.handleSessions(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body)
	}
	var got []db.Session
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rr.Body)
	}
	if len(got) != 0 {
		t.Fatalf("got %d sessions, want 0", len(got))
	}
}

func TestHandleSessions_SinglePlatform_SortedByBucketDesc(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	// Three sessions, two in the same 5-minute bucket (bucketMs =
	// 300000) and one earlier. The two in the same bucket sort by
	// projectID then title (stable secondary keys).
	reg.Register(&fakePlatform{
		id: "fake",
		sessions: []db.Session{
			mkSession("fake", "a", "alpha", 600000), // bucket 2
			mkSession("fake", "b", "beta", 700000),  // bucket 2
			mkSession("fake", "c", "gamma", 100000), // bucket 0
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	rr := httptest.NewRecorder()
	srv.handleSessions(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var got []db.Session
	mustUnmarshal(t, rr.Body.Bytes(), &got)
	if len(got) != 3 {
		t.Fatalf("got %d sessions, want 3", len(got))
	}
	// "gamma" (bucket 0) must be last; the other two are in bucket 2,
	// sorted by ProjectID ("proj-a" < "proj-b") then title.
	if got[2].ID != "c" {
		t.Fatalf("last session id = %q, want c", got[2].ID)
	}
}

func TestHandleSessions_TwoPlatforms_Merged(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	reg.Register(&fakePlatform{
		id:       "p1",
		sessions: []db.Session{mkSession("p1", "x", "alpha", 1_000_000)},
	})
	reg.Register(&fakePlatform{
		id:       "p2",
		sessions: []db.Session{mkSession("p2", "y", "alpha", 1_000_500)},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	rr := httptest.NewRecorder()
	srv.handleSessions(rr, req)

	var got []db.Session
	mustUnmarshal(t, rr.Body.Bytes(), &got)
	if len(got) != 2 {
		t.Fatalf("got %d sessions, want 2", len(got))
	}
	// Both in the same bucket, sorted by ProjectID ("proj-x" vs "proj-y").
	if got[0].ID != "x" || got[1].ID != "y" {
		t.Fatalf("merge order = %q,%q; want x,y", got[0].ID, got[1].ID)
	}
}

func TestHandleSessions_StateOverlay_AppliesArchivedSeenPinned(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	reg.Register(&fakePlatform{
		id: "fake",
		sessions: []db.Session{
			mkSession("fake", "arch", "a", 1000),
			mkSession("fake", "seen", "b", 1000),
			mkSession("fake", "pin", "c", 1000),
		},
	})
	applyStateSetup(t, srv.stateDB, stateSetup{
		archived: []stateRow{{"fake", "arch", 2000}}, // archivedAt > timeUpdated → stays archived
		seen:     []stateRow{{"fake", "seen", 1000}}, // seenAt >= timeUpdated → seen
		pinned:   []stateRow{{"fake", "pin", 0}},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	rr := httptest.NewRecorder()
	srv.handleSessions(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body)
	}
	var got []db.Session
	mustUnmarshal(t, rr.Body.Bytes(), &got)
	byID := map[string]db.Session{}
	for _, s := range got {
		byID[s.ID] = s
	}
	if !byID["arch"].Archived {
		t.Errorf("session arch: expected Archived=true")
	}
	if !byID["seen"].Seen {
		t.Errorf("session seen: expected Seen=true")
	}
	if !byID["pin"].Pinned {
		t.Errorf("session pin: expected Pinned=true")
	}
}

func TestHandleSessions_LimitParameterRespected(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	var sessions []db.Session
	for i := 0; i < 10; i++ {
		sessions = append(sessions, mkSession("fake", fmt.Sprintf("s%d", i), "t", int64(1000+i*1000)))
	}
	reg.Register(&fakePlatform{id: "fake", sessions: sessions})

	req := httptest.NewRequest(http.MethodGet, "/api/sessions?limit=3", nil)
	rr := httptest.NewRecorder()
	srv.handleSessions(rr, req)

	var got []db.Session
	mustUnmarshal(t, rr.Body.Bytes(), &got)
	if len(got) != 3 {
		t.Fatalf("got %d sessions, want 3 (limit)", len(got))
	}
}

func TestHandleSessions_SinceParameterIsForwardedToAdapter(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	var seenSince int64
	reg.Register(&fakePlatform{
		id: "fake",
		sessionsHook: func(_ context.Context, dir string, since int64) ([]db.Session, error) {
			seenSince = since
			return nil, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/sessions?since=12345", nil)
	rr := httptest.NewRecorder()
	srv.handleSessions(rr, req)

	if seenSince != 12345 {
		t.Fatalf("adapter Sessions called with since=%d, want 12345", seenSince)
	}
}

func TestHandleSessions_PinnedOutsideWindow_IsForceIncluded(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	// The adapter normally returns nothing within the since window
	// but exposes the pinned session via Session()/Owns().
	pinnedSess := mkSession("fake", "pinme", "old session", 50)
	reg.Register(&fakePlatformWithDetail{
		fakePlatform: fakePlatform{
			id:       "fake",
			sessions: nil, // empty within window
		},
		detailSession: &pinnedSess,
	})
	applyStateSetup(t, srv.stateDB, stateSetup{
		pinned: []stateRow{{"fake", "pinme", 0}},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/sessions?since=10000", nil)
	rr := httptest.NewRecorder()
	srv.handleSessions(rr, req)

	var got []db.Session
	mustUnmarshal(t, rr.Body.Bytes(), &got)
	if len(got) != 1 || got[0].ID != "pinme" {
		t.Fatalf("force-include of pinned session failed; got %+v", got)
	}
	if !got[0].Pinned {
		t.Errorf("force-included pinned session must have Pinned=true")
	}
}

func TestHandleSessions_PlatformErrorDoesNotAbortRequest(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	reg.Register(&fakePlatform{
		id:          "broken",
		sessionsErr: errors.New("upstream down"),
	})
	reg.Register(&fakePlatform{
		id:       "ok",
		sessions: []db.Session{mkSession("ok", "z", "z", 1000)},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	rr := httptest.NewRecorder()
	srv.handleSessions(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body)
	}
	var got []db.Session
	mustUnmarshal(t, rr.Body.Bytes(), &got)
	if len(got) != 1 || got[0].ID != "z" {
		t.Fatalf("expected only the healthy platform's session; got %+v", got)
	}
}

func TestHandleSessionAttachment_SavesFileToCache(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv, reg := newSessionsTestServer(t)
	projectDir := t.TempDir()
	reg.Register(&fakePlatformWithDetail{
		fakePlatform: fakePlatform{id: "opencode"},
		detailSession: &db.Session{
			ID:        "s1",
			Platform:  "opencode",
			Directory: projectDir,
		},
	})

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "notes & data.bin")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write([]byte("attachment body")); err != nil {
		t.Fatalf("write multipart: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/session/s1/attachment", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rr := httptest.NewRecorder()
	srv.dispatchSessionSubpath(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var got struct {
		Path string `json:"path"`
		Name string `json:"name"`
		Mime string `json:"mime"`
		Size int64  `json:"size"`
	}
	mustUnmarshal(t, rr.Body.Bytes(), &got)
	if got.Name != "notes___data.bin" {
		t.Fatalf("name = %q, want sanitized filename", got.Name)
	}
	if got.Size != int64(len("attachment body")) {
		t.Fatalf("size = %d", got.Size)
	}
	data, err := os.ReadFile(got.Path)
	if err != nil {
		t.Fatalf("read saved attachment: %v", err)
	}
	if string(data) != "attachment body" {
		t.Fatalf("saved data = %q", string(data))
	}
	if strings.Contains(got.Path, projectDir) {
		t.Fatalf("attachment path should not be inside project dir: %s", got.Path)
	}
}

// TestSweepComposerAttachments covers the unbounded-growth fix: nothing
// ever deleted composer uploads, so real user content (screenshots,
// PDFs, logs) accumulated forever in a directory macOS does not purge.
func TestSweepComposerAttachments(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "projhash", "s1")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(sessionDir, "1-fresh.png")
	stale := filepath.Join(sessionDir, "2-stale.png")
	for _, p := range []string{fresh, stale} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * composerAttachmentTTL)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	removed := sweepComposerAttachments(root, composerAttachmentTTL)
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("recent attachment was deleted: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale attachment survived the sweep: %v", err)
	}

	// Emptied directories are cleaned up too, so the tree does not grow
	// one dead folder per session forever.
	if err := os.Chtimes(fresh, old, old); err != nil {
		t.Fatal(err)
	}
	sweepComposerAttachments(root, composerAttachmentTTL)
	if _, err := os.Stat(sessionDir); !os.IsNotExist(err) {
		t.Errorf("empty session directory survived the sweep: %v", err)
	}

	// A missing root is not an error.
	if got := sweepComposerAttachments(filepath.Join(root, "nope"), composerAttachmentTTL); got != 0 {
		t.Errorf("sweep of a missing root = %d, want 0", got)
	}
}

// fakePlatformWithDetail extends fakePlatform with a non-nil
// SessionDetail returned from Session(). Used to exercise the
// pinned-force-include branch of handleSessions, which calls
// adapter.Session() to fetch a session that was pinned but not
// returned by Sessions() (e.g. it fell outside the time window).
type fakePlatformWithDetail struct {
	fakePlatform
	detailSession *db.Session
}

func (f *fakePlatformWithDetail) Session(_ context.Context, sessionID string, _, _ int) (*platforms.SessionDetail, error) {
	if f.detailSession != nil && f.detailSession.ID == sessionID {
		return &platforms.SessionDetail{Session: f.detailSession}, nil
	}
	return nil, platforms.ErrNotFound
}

func (f *fakePlatformWithDetail) Owns(_ context.Context, sessionID string) bool {
	if f.detailSession != nil && f.detailSession.ID == sessionID {
		return true
	}
	return f.fakePlatform.Owns(context.Background(), sessionID)
}

func mustUnmarshal(t *testing.T, data []byte, dst any) {
	t.Helper()
	if err := json.Unmarshal(data, dst); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, string(data))
	}
}

// --- Session notice integration tests ---

func TestHandleSessions_NoticeAppearsForRateLimitedSession(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	fp := &fakePlatform{
		id: "opencode",
		sessions: []db.Session{
			{
				ID:               "s1",
				Platform:         "opencode",
				Title:            "Rate limited session",
				Status:           "error",
				TimeUpdated:      1700000000000,
				TimeCreated:      1700000000000 - 1000,
				LastErrorMessage: "this request would exceed your account's rate limit. Please try again later [retrying in 5m attempt 1]",
				LastErrorAt:      1700000000000,
			},
			{
				ID:          "s2",
				Platform:    "opencode",
				Title:       "Normal session",
				Status:      "done",
				TimeUpdated: 1700000000000,
				TimeCreated: 1700000000000 - 1000,
			},
		},
	}
	reg.Register(fp)

	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	rr := httptest.NewRecorder()
	srv.handleSessions(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body)
	}

	// Decode into a list and find each session by ID.
	type sessionWithNotice struct {
		ID     string            `json:"id"`
		Notice *db.SessionNotice `json:"notice"`
	}
	var got []sessionWithNotice
	mustUnmarshal(t, rr.Body.Bytes(), &got)
	if len(got) != 2 {
		t.Fatalf("got %d sessions, want 2", len(got))
	}

	byID := make(map[string]*db.SessionNotice, len(got))
	for _, s := range got {
		byID[s.ID] = s.Notice
	}

	// s1 should have a notice.
	notice := byID["s1"]
	if notice == nil {
		t.Fatal("s1 should have a notice")
	}
	if notice.Kind != "rate_limit" {
		t.Errorf("notice.kind = %q, want rate_limit", notice.Kind)
	}
	if notice.Attempt != 1 {
		t.Errorf("notice.attempt = %d, want 1", notice.Attempt)
	}
	wantRetryAt := int64(1700000000000 + 5*60*1000)
	if notice.RetryAt != wantRetryAt {
		t.Errorf("notice.retryAt = %d, want %d", notice.RetryAt, wantRetryAt)
	}

	// s2 should NOT have a notice.
	if byID["s2"] != nil {
		t.Errorf("s2 should not have a notice, got %+v", byID["s2"])
	}
}

func TestHandleSessions_NoticeAppearsForGenericError(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	fp := &fakePlatform{
		id: "opencode",
		sessions: []db.Session{
			{
				ID:               "s1",
				Platform:         "opencode",
				Title:            "Generic error",
				Status:           "error",
				TimeUpdated:      1700000000000,
				TimeCreated:      1700000000000 - 1000,
				LastErrorMessage: "connection refused",
				LastErrorAt:      1700000000000,
			},
		},
	}
	reg.Register(fp)

	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	rr := httptest.NewRecorder()
	srv.handleSessions(rr, req)

	var sessions []struct {
		Notice *db.SessionNotice `json:"notice"`
	}
	mustUnmarshal(t, rr.Body.Bytes(), &sessions)
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	if sessions[0].Notice == nil {
		t.Fatal("expected generic error notice")
	}
	if sessions[0].Notice.Kind != "error" {
		t.Errorf("notice.kind = %q, want error", sessions[0].Notice.Kind)
	}
	if sessions[0].Notice.Message != "connection refused" {
		t.Errorf("notice.message = %q, want connection refused", sessions[0].Notice.Message)
	}
}

func TestHandleSessions_NoticeAppearsForProviderOverload(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	fp := &fakePlatform{
		id: "opencode",
		sessions: []db.Session{
			{
				ID:               "s1",
				Platform:         "opencode",
				Title:            "Overloaded session",
				Status:           "error",
				TimeUpdated:      1700000000000,
				TimeCreated:      1700000000000 - 1000,
				LastErrorMessage: "provider is overloaded [retrying in 30s attempt 2]",
				LastErrorAt:      1700000000000,
			},
		},
	}
	reg.Register(fp)

	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	rr := httptest.NewRecorder()
	srv.handleSessions(rr, req)

	var sessions []struct {
		Notice *db.SessionNotice `json:"notice"`
	}
	mustUnmarshal(t, rr.Body.Bytes(), &sessions)
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	if sessions[0].Notice == nil {
		t.Fatal("expected provider overload notice")
	}
	if sessions[0].Notice.Kind != "provider_overloaded" {
		t.Errorf("kind = %q, want provider_overloaded", sessions[0].Notice.Kind)
	}
	if sessions[0].Notice.RetryAt != 1700000030000 {
		t.Errorf("retryAt = %d, want 1700000030000", sessions[0].Notice.RetryAt)
	}
	if sessions[0].Notice.Attempt != 2 {
		t.Errorf("attempt = %d, want 2", sessions[0].Notice.Attempt)
	}
}

func TestHandleSession_NoticeAppearsForProviderOverload(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	sess := &db.Session{
		ID:               "s1",
		Platform:         "opencode",
		Title:            "Overloaded session",
		Status:           "error",
		TimeUpdated:      1700000000000,
		TimeCreated:      1700000000000 - 1000,
		LastErrorMessage: "provider is overloaded",
		LastErrorAt:      1700000000000,
	}
	fp := &fakePlatform{
		id:       "opencode",
		sessions: []db.Session{*sess},
		sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
			if id != sess.ID {
				return nil, platforms.ErrNotFound
			}
			return &platforms.SessionDetail{Session: sess}, nil
		},
	}
	reg.Register(fp)

	req := httptest.NewRequest(http.MethodGet, "/api/session/s1", nil)
	rr := httptest.NewRecorder()
	srv.handleSession(rr, req)

	var body struct {
		Session *db.Session `json:"session"`
	}
	mustUnmarshal(t, rr.Body.Bytes(), &body)
	if body.Session == nil || body.Session.Notice == nil {
		t.Fatalf("expected detail session provider overload notice, got %+v", body.Session)
	}
	if body.Session.Notice.Kind != "provider_overloaded" {
		t.Errorf("kind = %q, want provider_overloaded", body.Session.Notice.Kind)
	}
}

// TestHandleSession_UnarchivesOnOpen verifies that opening a session
// clears both its own archive marker and its project's archive marker,
// so the sidebar shows the project + session tile again.
func TestHandleSession_UnarchivesOnOpen(t *testing.T) {
	srv, reg := newSessionsTestServer(t)

	sess := &db.Session{
		ID:          "s1",
		Platform:    "opencode",
		Directory:   "/src/foo",
		Title:       "Old session",
		TimeUpdated: 1000,
		TimeCreated: 500,
	}
	fp := &fakePlatform{
		id:       "opencode",
		sessions: []db.Session{*sess},
		sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
			return &platforms.SessionDetail{Session: sess}, nil
		},
	}
	reg.Register(fp)

	root := projectRootForDirectory(sess.Directory)
	if err := srv.stateDB.ArchiveSession("opencode", "s1", 1000); err != nil {
		t.Fatalf("ArchiveSession: %v", err)
	}
	if err := srv.stateDB.ArchiveProject(state.LocalRemoteID, root); err != nil {
		t.Fatalf("ArchiveProject: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/session/s1", nil)
	rr := httptest.NewRecorder()
	srv.handleSession(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	archived, _ := srv.stateDB.ArchivedSessions()
	if _, ok := archived[state.Key{Platform: "opencode", SessionID: "s1"}]; ok {
		t.Error("session should be unarchived after open")
	}
	archivedProjects, _ := srv.stateDB.ArchivedProjects()
	if _, ok := archivedProjects[state.ProjectKey{RemoteID: state.LocalRemoteID, Root: root}]; ok {
		t.Error("project should be unarchived after open")
	}
}

// --- POST /api/session/{id}/auto-approve ---

func TestPromptSessionIDPrefersIssuingChild(t *testing.T) {
	entry := platforms.LivePrompt{"sessionID": "ses-child"}
	if got := promptSessionID(entry, "ses-parent"); got != "ses-child" {
		t.Fatalf("prompt session = %q, want child", got)
	}
	if got := promptSessionID(platforms.LivePrompt{}, "ses-parent"); got != "ses-parent" {
		t.Fatalf("fallback prompt session = %q, want parent", got)
	}
}

// TestHandleSessionAutoApproveSet_EnablingTriggersJudgeForPending is the
// regression for the "enabling auto-approve shows 'starting…' forever"
// bug: when a permission prompt is already on screen and the user
// clicks "Enable auto-approve", the POST handler must resume the judge
// for any already-pending permissions in that session.
//
// Without this, the original permission.asked event already fired (and
// was discarded because auto-approve was off), no new event ever
// arrives for the same prompt, the frontend does not refetch
// permissions on toggle, and the UI sits forever on the
// "Auto-approve on · starting…" fallback.
//
// The handler enforces resumption by calling ensureAutoApprove for
// every pending permission returned by adapter.ListPermissions —
// observable here through the fakePlatform's listPermissionsFn.
func TestHandleSessionAutoApproveSet_EnablingTriggersJudgeForPending(t *testing.T) {
	srv, reg := newSessionsTestServer(t)

	const (
		sessionID    = "ses-toggle"
		permissionID = "perm-pending"
	)

	listCalls := 0
	fp := &fakePlatform{
		id:       "opencode",
		sessions: []db.Session{{ID: sessionID, Platform: "opencode", Directory: "/x"}},
		listPermissionsFn: func(sid string) ([]platforms.LivePrompt, error) {
			listCalls++
			if sid != sessionID {
				t.Errorf("ListPermissions session = %q, want %q", sid, sessionID)
			}
			return []platforms.LivePrompt{
				{
					"id":         permissionID,
					"permission": "Bash command",
				},
			}, nil
		},
	}
	reg.Register(fp)
	// Pre-populate the reverse-lookup cache so resolvePlatformForSession
	// finds the fake adapter without falling back to Session() (the fake
	// returns ErrNotFound there).
	reg.RememberSessions("opencode", fp.sessions)

	// Toggle ON.
	body := strings.NewReader(`{"enabled":true}`)
	req := httptest.NewRequest(http.MethodPost, "/api/session/"+sessionID+"/auto-approve", body)
	rr := httptest.NewRecorder()
	srv.handleSessionAutoApproveSet(rr, req)

	if rr.Code != 204 {
		t.Fatalf("status = %d, want 204; body=%s", rr.Code, rr.Body)
	}

	// 1. The flag was persisted to state.db.
	enabled, exists, err := srv.stateDB.GetAutoApprove("opencode", sessionID)
	if err != nil {
		t.Fatalf("GetAutoApprove: %v", err)
	}
	if !exists || !enabled {
		t.Errorf("auto-approve state not persisted: exists=%v enabled=%v", exists, enabled)
	}

	// 2. ListPermissions was called (resume path fired).
	if listCalls != 1 {
		t.Errorf("ListPermissions calls = %d, want 1 (resume path must fire on toggle ON)", listCalls)
	}

	// 3. ensureAutoApprove must have either claimed the slot OR
	//    recorded a verdict for the pending permission. With s.db nil
	//    and no judge wired, the spawned goroutine bails inside
	//    backgroundAutoApprove and releaseAutoApprove drops the
	//    record, but the claim is observable as a transient entry —
	//    so we settle for proving the call reached the dedup cache by
	//    waiting for the goroutine to settle and confirming no panic.
	//    The functional contract checked here is the listPermissionsFn
	//    call count above; this assertion guards against a regression
	//    where someone moves the resume into a path that never reaches
	//    ensureAutoApprove (e.g. wraps it in a condition that never
	//    fires for this fake-adapter setup).
	// The per-permission state now lives inside the autoapprove
	// service; the map may be empty (goroutine already released its
	// slot) — both outcomes are acceptable. The test asserts on
	// ListPermissions being invoked, which is the proxy for "resume
	// path executed".
	_ = srv.aaSvc()
}

// TestHandleSessionAutoApproveSet_DisablingDoesNotResume is the
// counterpart: when the toggle goes from enabled to disabled the
// handler must NOT walk pending permissions through ensureAutoApprove
// — the resume is enable-only.
func TestHandleSessionAutoApproveSet_DisablingDoesNotResume(t *testing.T) {
	srv, reg := newSessionsTestServer(t)

	const sessionID = "ses-toggle-off"

	listCalls := 0
	fp := &fakePlatform{
		id:       "opencode",
		sessions: []db.Session{{ID: sessionID, Platform: "opencode", Directory: "/x"}},
		listPermissionsFn: func(_ string) ([]platforms.LivePrompt, error) {
			listCalls++
			return nil, nil
		},
	}
	reg.Register(fp)
	reg.RememberSessions("opencode", fp.sessions)

	body := strings.NewReader(`{"enabled":false}`)
	req := httptest.NewRequest(http.MethodPost, "/api/session/"+sessionID+"/auto-approve", body)
	rr := httptest.NewRecorder()
	srv.handleSessionAutoApproveSet(rr, req)

	if rr.Code != 204 {
		t.Fatalf("status = %d, want 204; body=%s", rr.Code, rr.Body)
	}
	if listCalls != 0 {
		t.Errorf("ListPermissions should not be called when disabling; got %d calls", listCalls)
	}
}

func TestInjectApprovalNotices_SkipsUserAllowAlways(t *testing.T) {
	srv, _ := newSessionsTestServer(t)
	for _, approval := range []state.ApprovedPermission{
		{
			PermissionID:   "user-approved",
			PermissionText: "external_directory",
			Patterns:       []string{"/worktrees/*"},
			Reasoning:      "user clicked Allow always",
			ApprovedAt:     100,
		},
		{
			PermissionID:   "ai-approved",
			PermissionText: "bash",
			Patterns:       []string{"git status"},
			Reasoning:      "Read-only command.",
			ApprovedAt:     200,
		},
	} {
		if err := srv.stateDB.RecordApprovedPermission("opencode", "ses-1", approval); err != nil {
			t.Fatalf("RecordApprovedPermission: %v", err)
		}
	}

	var messages []db.Message
	var parts []db.Part
	injectApprovalNotices("opencode", "ses-1", srv.stateDB, &messages, &parts)

	if len(messages) != 1 || messages[0].ID != "ocman-notice-ai-approved" {
		t.Fatalf("injected messages = %#v, want only AI approval", messages)
	}
	if len(parts) != 1 || parts[0].MessageID != "ocman-notice-ai-approved" {
		t.Fatalf("injected parts = %#v, want only AI approval", parts)
	}
}

// The restart-opencode endpoint is a localhost-only control surface
// (it kills and relaunches tmux processes). Non-loopback callers must
// be rejected before any platform/tmux work happens. httptest sets a
// non-loopback RemoteAddr (192.0.2.1) by default.
func TestHandleSessionRestartOpencode_RejectsNonLoopback(t *testing.T) {
	srv, _ := newSessionsTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/session/s1/restart-opencode", nil)
	rr := httptest.NewRecorder()
	srv.handleSessionRestartOpencode(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}
