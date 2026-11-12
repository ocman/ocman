package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
)

func TestExplicitRestoreClearsOwningProjectArchive(t *testing.T) {
	for _, owner := range []string{state.LocalRemoteID, "box"} {
		for _, snoozed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/snoozed=%v", owner, snoozed), func(t *testing.T) {
				srv, reg := newSessionsTestServer(t)
				platform := "opencode"
				if owner != state.LocalRemoteID {
					platform = platforms.CompoundPlatformID(owner, platform)
				}
				row := db.Session{ID: "s1", Platform: platform, Directory: "/src/.worktrees/repo/topic", RemoteID: owner, TimeUpdated: 1, Status: db.StatusBusy}
				reg.Register(&fakePlatform{PlatformID: platform, SessionList: []db.Session{row}, SessionFn: func(string) (*platforms.SessionDetail, error) {
					copy := row
					return &platforms.SessionDetail{Session: &copy}, nil
				}})
				for _, host := range []string{state.LocalRemoteID, "box", "other"} {
					if err := srv.stateDB.ArchiveProject(t.Context(), host, "/src/repo"); err != nil {
						t.Fatal(err)
					}
				}
				if snoozed {
					if err := srv.stateDB.SnoozeSession(t.Context(), platform, "s1", 9999999999999); err != nil {
						t.Fatal(err)
					}
				}
				body := fmt.Sprintf(`{"platform":%q,"sessionId":"s1","archived":false}`, platform)
				response := httptest.NewRecorder()
				srv.handleArchiveSession(response, httptest.NewRequest(http.MethodPost, "/api/session/archive", strings.NewReader(body)))
				if response.Code != http.StatusOK {
					t.Fatalf("restore: %d %s", response.Code, response.Body.String())
				}
				response = httptest.NewRecorder()
				serveSessionRoute(srv.handleSession, response, httptest.NewRequest(http.MethodGet, "/api/session/s1?platform="+platform+"&peek=1", nil))
				var detail struct{ Session db.Session }
				if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &detail) != nil {
					t.Fatalf("read: %d %s", response.Code, response.Body.String())
				}
				if detail.Session.Archived || detail.Session.SnoozedUntil != 0 {
					t.Fatalf("restored session hidden again: %+v", detail.Session)
				}
				rows := []db.Session{row}
				if err := srv.applySessionState(t.Context(), rows); err != nil || rows[0].Archived {
					t.Fatalf("list overlay: archived=%v err=%v", rows[0].Archived, err)
				}
				projects, err := srv.stateDB.ArchivedProjects(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				for _, host := range []string{state.LocalRemoteID, "box", "other"} {
					if _, archived := projects[state.ProjectKey{RemoteID: host, Root: "/src/repo"}]; archived != (host != owner) {
						t.Fatalf("project owner %s archived=%v", host, archived)
					}
				}
			})
		}
	}
}

func TestExplicitRestoreFailsClosed(t *testing.T) {
	for _, failure := range []string{"closed store", "missing owner", "missing session", "read failure", "missing provenance"} {
		t.Run(failure, func(t *testing.T) {
			srv, reg := newSessionsTestServer(t)
			if err := srv.stateDB.ArchiveProject(t.Context(), "box", "/repo"); err != nil {
				t.Fatal(err)
			}
			if err := srv.stateDB.SnoozeSession(t.Context(), "r-box:opencode", "s1", 9999999999999); err != nil {
				t.Fatal(err)
			}
			if failure == "closed store" {
				if err := srv.stateDB.Close(); err != nil {
					t.Fatal(err)
				}
			} else if failure != "missing owner" {
				reg.Register(&fakePlatform{PlatformID: "r-box:opencode", SessionFn: func(string) (*platforms.SessionDetail, error) {
					if failure == "read failure" {
						return nil, errors.New("owner disconnected")
					}
					return &platforms.SessionDetail{}, nil
				}})
			}
			platform := "r-box:opencode"
			if failure == "missing provenance" {
				platform = "r-box:missing-account"
			}
			if err := srv.setSessionArchived(t.Context(), platform, "s1", 0, false); err == nil {
				t.Fatal("restore should fail")
			}
			if failure != "closed store" {
				snoozed, err := srv.stateDB.SnoozedSessions(t.Context())
				if err != nil || snoozed[state.Key{Platform: "r-box:opencode", SessionID: "s1"}] == 0 {
					t.Fatalf("failed restore changed snooze: %v, %v", snoozed, err)
				}
			}
		})
	}
}
