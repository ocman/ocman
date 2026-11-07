package server

import (
	"errors"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcptest"
	"github.com/ocman/ocman/internal/db"
	internalmcp "github.com/ocman/ocman/internal/mcp"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/testutil"
)

// archiveTarget registers platform with one session whose status the test
// controls; reads counts Session calls so a test can flip status mid-call.
func archiveTarget(t *testing.T, reg *platforms.Registry, platform string, status func(reads int) db.SessionStatus) {
	t.Helper()
	reads := 0
	reg.Register(&fakePlatform{PlatformID: platform, SessionFn: func(id string) (*platforms.SessionDetail, error) {
		if id != "target" {
			return nil, platforms.ErrNotFound
		}
		reads++
		return &platforms.SessionDetail{Session: &db.Session{ID: id, TimeUpdated: 1000, Status: status(reads)}}, nil
	}})
}

func archivedStamp(t *testing.T, srv *Server, platform string) (int64, bool) {
	t.Helper()
	archived, err := srv.stateDB.ArchivedSessions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	stamp, ok := archived[state.Key{Platform: platform, SessionID: "target"}]
	return stamp, ok
}

func TestMCPArchiveIdleSession(t *testing.T) {
	for _, platform := range []string{"opencode", "r-machine:opencode"} {
		t.Run(platform, func(t *testing.T) {
			srv, reg := newSessionsTestServer(t)
			archiveTarget(t, reg, platform, func(int) db.SessionStatus { return db.StatusDone })
			svc := sessionMCPService{srv}
			before := time.Now().UnixMilli()
			for _, scope := range []string{platform, ""} {
				got, err := svc.SetSessionArchived(t.Context(), scope, "target", true)
				if err != nil || got != (internalmcp.ArchivedSession{Platform: platform, SessionID: "target", Archived: true}) {
					t.Fatalf("archive = %#v, %v", got, err)
				}
			}
			stamp, ok := archivedStamp(t, srv, platform)
			// Local stamps clamp to the hub's now; remote ones keep the owner's clock.
			if !ok || (platform == "opencode" && stamp < before) || (platform != "opencode" && stamp != 1000) {
				t.Fatalf("stamp = %d, %v", stamp, ok)
			}
			got, err := svc.SetSessionArchived(t.Context(), platform, "target", false)
			if err != nil || got.Archived {
				t.Fatalf("unarchive = %#v, %v", got, err)
			}
			if _, ok := archivedStamp(t, srv, platform); ok {
				t.Fatal("session still archived")
			}
		})
	}
}

func TestMCPArchiveBusySessionDefersToIdle(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	archiveTarget(t, reg, "opencode", func(int) db.SessionStatus { return db.StatusBusy })
	svc := sessionMCPService{srv}
	got, err := svc.SetSessionArchived(t.Context(), "opencode", "target", true)
	if err != nil || got != (internalmcp.ArchivedSession{Platform: "opencode", SessionID: "target", Deferred: true}) {
		t.Fatalf("archive = %#v, %v", got, err)
	}
	if _, ok := archivedStamp(t, srv, "opencode"); ok {
		t.Fatal("busy session archived before its turn finished")
	}
	srv.onSessionIdle("opencode", "other")
	if _, ok := archivedStamp(t, srv, "opencode"); ok {
		t.Fatal("another session's idle edge applied the archive")
	}
	idleAt := time.Now()
	srv.onSessionIdle("opencode", "target")
	stamp, ok := archivedStamp(t, srv, "opencode")
	// The stamp leaves room for the write agents make just after going idle.
	if !ok || stamp < idleAt.Add(deferredArchiveGrace).UnixMilli() {
		t.Fatalf("stamp = %d, %v", stamp, ok)
	}
	if _, err := svc.SetSessionArchived(t.Context(), "opencode", "target", false); err != nil {
		t.Fatal(err)
	}
	srv.onSessionIdle("opencode", "target")
	if _, ok := archivedStamp(t, srv, "opencode"); ok {
		t.Fatal("a consumed deferred archive applied twice")
	}
}

func TestMCPUnarchiveCancelsDeferredArchive(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	archiveTarget(t, reg, "opencode", func(int) db.SessionStatus { return db.StatusBusy })
	svc := sessionMCPService{srv}
	if got, err := svc.SetSessionArchived(t.Context(), "opencode", "target", true); err != nil || !got.Deferred {
		t.Fatalf("archive = %#v, %v", got, err)
	}
	if _, err := svc.SetSessionArchived(t.Context(), "opencode", "target", false); err != nil {
		t.Fatal(err)
	}
	srv.onSessionIdle("opencode", "target")
	if _, ok := archivedStamp(t, srv, "opencode"); ok {
		t.Fatal("unarchive did not cancel the deferred archive")
	}
}

func TestMCPArchiveSessionIdleDuringRequest(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	// Busy on resolution, idle on the recheck: its idle edge has already passed.
	archiveTarget(t, reg, "opencode", func(reads int) db.SessionStatus {
		if reads == 1 {
			return db.StatusBusy
		}
		return db.StatusDone
	})
	before := time.Now()
	got, err := (sessionMCPService{srv}).SetSessionArchived(t.Context(), "opencode", "target", true)
	if err != nil || !got.Archived || got.Deferred {
		t.Fatalf("archive = %#v, %v", got, err)
	}
	if stamp, ok := archivedStamp(t, srv, "opencode"); !ok || stamp < before.Add(deferredArchiveGrace).UnixMilli() {
		t.Fatalf("stamp = %d, %v", stamp, ok)
	}
}

func TestMCPArchiveRejectsBusyRemoteAndDisconnectedOwner(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	archiveTarget(t, reg, "r-machine:opencode", func(int) db.SessionStatus { return db.StatusBusy })
	svc := sessionMCPService{srv}
	if _, err := svc.SetSessionArchived(t.Context(), "r-machine:opencode", "target", true); !errors.Is(err, internalmcp.ErrArchiveBusyRemote) {
		t.Fatalf("busy remote = %v", err)
	}
	for _, scope := range []string{"r-disconnected:opencode", "r-machine:opencode"} {
		id := "target"
		if scope == "r-machine:opencode" {
			id = "missing"
		}
		if _, err := svc.SetSessionArchived(t.Context(), scope, id, true); !errors.Is(err, platforms.ErrNotFound) {
			t.Fatalf("%s/%s = %v", scope, id, err)
		}
	}
	if archived, err := srv.stateDB.ArchivedSessions(t.Context()); err != nil || len(archived) != 0 {
		t.Fatalf("archived = %#v, %v", archived, err)
	}
	reg.Register(&fakePlatform{PlatformID: "r-down:opencode", SessionFn: func(string) (*platforms.SessionDetail, error) {
		return nil, platforms.ErrPlatformUnreachable
	}})
	if _, err := svc.SetSessionArchived(t.Context(), "r-down:opencode", "target", false); !errors.Is(err, platforms.ErrPlatformUnreachable) {
		t.Fatalf("unreachable owner = %v", err)
	}
}

func TestMCPArchiveWithoutStateDB(t *testing.T) {
	srv, _ := newSessionsTestServer(t)
	srv.stateDB = nil
	if _, err := (sessionMCPService{srv}).SetSessionArchived(t.Context(), "opencode", "target", true); err == nil {
		t.Fatal("archive without state db succeeded")
	}
}

func TestMCPArchiveSessionToolDelivery(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	archiveTarget(t, reg, "test", func(int) db.SessionStatus { return db.StatusDone })
	mcpServer, err := mcptest.NewServer(t, internalmcp.ServerTools(internalmcp.Deps{SessionService: sessionMCPService{srv}})...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mcpServer.Close)
	result := testutil.CallTool(t, mcpServer, "sessions", map[string]any{"action": "archive", "session_id": "target"})
	if _, ok := archivedStamp(t, srv, "test"); result.IsError || !ok {
		t.Fatalf("tool result = %q, archived = %v", testutil.ResultText(result), ok)
	}
}
